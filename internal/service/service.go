package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"dodo-payments/internal/apperr"
	"dodo-payments/internal/logging"
	"dodo-payments/internal/model"
	"dodo-payments/internal/payment"
	"dodo-payments/internal/repository"
)

type Service struct {
	repo        *repository.Repository
	psp         payment.PSP
	webhookHTTP *http.Client
	logger      *logging.Logger
}

func New(repo *repository.Repository, psp payment.PSP, logger *logging.Logger) *Service {
	svc := &Service{
		repo:        repo,
		psp:         psp,
		webhookHTTP: &http.Client{Timeout: 10 * time.Second},
		logger:      logger,
	}
	return svc
}

func (s *Service) ListCustomers(ctx context.Context, businessID string) ([]model.Customer, error) {
	return s.repo.ListCustomers(ctx, businessID)
}

func (s *Service) CreateCustomer(ctx context.Context, businessID, name, email string) (model.Customer, error) {
	return s.repo.CreateCustomer(ctx, businessID, strings.TrimSpace(name), strings.TrimSpace(email))
}

func (s *Service) ListInvoices(ctx context.Context, businessID, state string) ([]model.Invoice, error) {
	return s.repo.ListInvoices(ctx, businessID, state)
}

func (s *Service) GetInvoice(ctx context.Context, businessID, invoiceID string) (model.Invoice, error) {
	return s.repo.GetInvoice(ctx, businessID, invoiceID)
}

func (s *Service) GetLatestPaymentAttempt(ctx context.Context, businessID, invoiceID string) (model.PaymentAttempt, error) {
	return s.repo.LatestPaymentAttempt(ctx, businessID, invoiceID)
}

func (s *Service) CreateInvoice(ctx context.Context, businessID, customerID, currency, dueDate string, items []model.LineItem) (model.Invoice, error) {
	if customerID == "" || currency == "" || dueDate == "" || len(items) == 0 {
		return model.Invoice{}, apperr.BadRequest("invalid_request", "customer_id, currency, due_date, and line_items are required")
	}
	if !model.SupportedCurrency(currency) {
		return model.Invoice{}, apperr.BadRequest("invalid_currency", "currency must be one of USD, GBP, INR, or EUR")
	}
	if _, err := time.Parse("2006-01-02", dueDate); err != nil {
		return model.Invoice{}, apperr.BadRequest("invalid_request", "due_date must be YYYY-MM-DD")
	}
	var total int64
	for _, item := range items {
		if strings.TrimSpace(item.Description) == "" || item.Quantity <= 0 || item.UnitAmountCents <= 0 {
			return model.Invoice{}, apperr.BadRequest("invalid_request", "each line item requires description, quantity > 0, and unit_amount_cents > 0")
		}
		if item.Quantity > math.MaxInt64/item.UnitAmountCents {
			return model.Invoice{}, apperr.BadRequest("invalid_request", "line item amount exceeds supported range")
		}
		lineTotal := item.Quantity * item.UnitAmountCents
		if total > math.MaxInt64-lineTotal {
			return model.Invoice{}, apperr.BadRequest("invalid_request", "invoice total exceeds supported range")
		}
		total += lineTotal
	}
	invoice, err := s.repo.CreateInvoice(ctx, businessID, customerID, currency, dueDate, total, items)
	if errors.Is(err, apperr.ErrNotFound) {
		return model.Invoice{}, apperr.BadRequest("invalid_customer", "customer does not belong to this business")
	}
	if err == nil {
		s.logger.Info(ctx, "invoice created",
			slog.String("business_id", businessID),
			slog.String("invoice_id", invoice.ID),
			slog.String("customer_id", customerID),
			slog.String("currency", currency),
			slog.Int64("total_cents", total),
			slog.Int("line_item_count", len(items)),
		)
	}
	return invoice, err
}

func (s *Service) VoidInvoice(ctx context.Context, businessID, invoiceID string) error {
	err := s.repo.VoidInvoice(ctx, businessID, invoiceID)
	if err == nil {
		s.logger.Info(ctx, "invoice voided",
			slog.String("business_id", businessID),
			slog.String("invoice_id", invoiceID),
		)
	}
	return err
}

type PaymentResponse struct {
	Status int
	Body   json.RawMessage
}

func (s *Service) PayInvoice(ctx context.Context, businessID, invoiceID, key, token string) (PaymentResponse, error) {
	requestHash := hashString(businessID + "|" + invoiceID + "|" + token)
	record, err := s.repo.FindIdempotency(ctx, businessID, invoiceID, key)
	if err != nil {
		return PaymentResponse{}, err
	}
	if record != nil {
		response, err := replayIdempotency(record, requestHash)
		if err == nil {
			s.logger.Info(ctx, "payment response replayed",
				slog.String("business_id", businessID),
				slog.String("invoice_id", invoiceID),
				slog.Int("http_status", response.Status),
			)
		}
		return response, err
	}

	attemptID, amount, currency, err := s.repo.BeginPayment(ctx, businessID, invoiceID, key, token, requestHash)
	if err != nil {
		// A concurrent retry may have inserted the same key while this request waited on the invoice lock.
		record, lookupErr := s.repo.FindIdempotency(ctx, businessID, invoiceID, key)
		if lookupErr != nil {
			return PaymentResponse{}, errors.Join(err, lookupErr)
		}
		if record != nil {
			response, replayErr := replayIdempotency(record, requestHash)
			if replayErr == nil {
				s.logger.Info(ctx, "payment response replayed",
					slog.String("business_id", businessID),
					slog.String("invoice_id", invoiceID),
					slog.Int("http_status", response.Status),
				)
			}
			return response, replayErr
		}
		return PaymentResponse{}, err
	}

	s.logger.Info(ctx, "payment attempt started",
		slog.String("business_id", businessID),
		slog.String("invoice_id", invoiceID),
		slog.String("attempt_id", attemptID),
		slog.String("currency", currency),
		slog.Int64("amount_cents", amount),
	)
	pspStarted := time.Now()
	result, pspErr := s.psp.Charge(ctx, token, amount, currency, pspIdempotencyKey(businessID, invoiceID, key))
	if pspErr != nil {
		s.logger.Info(ctx, "payment provider result unknown",
			slog.String("business_id", businessID),
			slog.String("invoice_id", invoiceID),
			slog.String("attempt_id", attemptID),
			slog.Int64("psp_duration_ms", time.Since(pspStarted).Milliseconds()),
		)
		body, err := s.repo.MarkPaymentPending(ctx, invoiceID, businessID, attemptID, key, amount, currency, pspErr)
		if err != nil {
			return PaymentResponse{}, err
		}
		s.logger.Info(ctx, "payment attempt marked pending",
			slog.String("business_id", businessID),
			slog.String("invoice_id", invoiceID),
			slog.String("attempt_id", attemptID),
		)
		return PaymentResponse{Status: http.StatusAccepted, Body: body}, nil
	}

	state, paymentStatus, responseCode := "open", "failed", http.StatusOK
	code, pspRef := result.Code, result.PSPReference
	if result.Status == "succeeded" {
		state, paymentStatus, code = "paid", "succeeded", ""
	}
	s.logger.Info(ctx, "payment provider responded",
		slog.String("business_id", businessID),
		slog.String("invoice_id", invoiceID),
		slog.String("attempt_id", attemptID),
		slog.String("payment_status", paymentStatus),
		slog.String("provider_code", code),
		slog.Int64("psp_duration_ms", time.Since(pspStarted).Milliseconds()),
	)
	body, err := s.repo.FinishPayment(ctx, businessID, invoiceID, attemptID, key, state, paymentStatus, code, pspRef, amount, currency, responseCode)
	if err != nil {
		return PaymentResponse{}, err
	}
	s.logger.Info(ctx, "payment attempt completed",
		slog.String("business_id", businessID),
		slog.String("invoice_id", invoiceID),
		slog.String("attempt_id", attemptID),
		slog.String("payment_status", paymentStatus),
		slog.String("invoice_state", state),
		slog.String("provider_code", code),
	)
	return PaymentResponse{Status: responseCode, Body: body}, nil
}

func replayIdempotency(record *model.IdempotencyRecord, requestHash string) (PaymentResponse, error) {
	if record.RequestHash != requestHash {
		return PaymentResponse{}, apperr.ErrIdempotencyConflict
	}
	body := record.ResponseBody
	if len(body) == 0 {
		body = json.RawMessage(`{}`)
	}
	return PaymentResponse{Status: record.ResponseCode, Body: body}, nil
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func pspIdempotencyKey(businessID, invoiceID, key string) string {
	return hashString(businessID + "|" + invoiceID + "|" + key)
}

func (s *Service) CreateWebhookEndpoint(ctx context.Context, businessID, url, secret string) (model.WebhookEndpoint, error) {
	if strings.TrimSpace(url) == "" || strings.TrimSpace(secret) == "" {
		return model.WebhookEndpoint{}, apperr.BadRequest("invalid_request", "url and secret are required")
	}
	return s.repo.CreateWebhookEndpoint(ctx, businessID, strings.TrimSpace(url), secret)
}

func (s *Service) ListWebhookEndpoints(ctx context.Context, businessID string) ([]model.WebhookEndpoint, error) {
	return s.repo.ListWebhookEndpoints(ctx, businessID)
}

func (s *Service) StartWebhookWorker(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			if err := s.deliverPendingWebhooks(ctx); err != nil {
				s.logger.Error(ctx, "webhook worker failed", err)
			}
			if err := s.reconcilePendingPayments(ctx); err != nil {
				s.logger.Error(ctx, "pending payment reconciliation failed", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (s *Service) reconcilePendingPayments(ctx context.Context) error {
	attempts, err := s.repo.ClaimPendingPayments(ctx, 10)
	if err != nil {
		return err
	}
	for _, attempt := range attempts {
		s.logger.Info(ctx, "pending payment retry started",
			slog.String("business_id", attempt.BusinessID),
			slog.String("invoice_id", attempt.InvoiceID),
			slog.String("attempt_id", attempt.ID),
			slog.Int("retry_count", attempt.RetryCount),
		)
		result, err := s.psp.Charge(ctx, attempt.Token, attempt.AmountCents, attempt.Currency,
			pspIdempotencyKey(attempt.BusinessID, attempt.InvoiceID, attempt.IdempotencyKey))
		if err != nil {
			if _, markErr := s.repo.MarkPaymentPending(ctx, attempt.InvoiceID, attempt.BusinessID, attempt.ID, attempt.IdempotencyKey, attempt.AmountCents, attempt.Currency, err); markErr != nil {
				return errors.Join(err, markErr)
			}
			s.logger.Info(ctx, "pending payment retry remains pending",
				slog.String("business_id", attempt.BusinessID),
				slog.String("invoice_id", attempt.InvoiceID),
				slog.String("attempt_id", attempt.ID),
				slog.Int("retry_count", attempt.RetryCount+1),
			)
			continue
		}
		state, paymentStatus := "open", "failed"
		code := result.Code
		if result.Status == "succeeded" {
			state, paymentStatus, code = "paid", "succeeded", ""
		}
		if _, err := s.repo.FinishPayment(ctx, attempt.BusinessID, attempt.InvoiceID, attempt.ID, attempt.IdempotencyKey, state, paymentStatus, code, result.PSPReference, attempt.AmountCents, attempt.Currency, http.StatusOK); err != nil {
			return err
		}
		s.logger.Info(ctx, "pending payment retry completed",
			slog.String("business_id", attempt.BusinessID),
			slog.String("invoice_id", attempt.InvoiceID),
			slog.String("attempt_id", attempt.ID),
			slog.String("payment_status", paymentStatus),
			slog.String("invoice_state", state),
			slog.String("provider_code", code),
		)
	}
	return nil
}

func (s *Service) deliverPendingWebhooks(ctx context.Context) error {
	events, err := s.repo.ClaimWebhookEvents(ctx, 10)
	if err != nil {
		return err
	}
	for _, event := range events {
		deliveryStarted := time.Now()
		webhookAttrs := []slog.Attr{
			slog.String("business_id", event.BusinessID),
			slog.String("event_id", event.ID),
			slog.String("event_type", event.EventType),
			slog.Int("attempt", event.Attempts+1),
		}
		retryMessage := "webhook delivery retry scheduled"
		if event.Attempts+1 >= 5 {
			retryMessage = "webhook delivery failed"
		}
		s.logger.Info(ctx, "webhook delivery started", webhookAttrs...)
		endpoint, err := s.repo.WebhookEndpointForBusiness(ctx, event.BusinessID)
		if err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				if err := s.repo.MarkWebhookSent(ctx, event.ID); err != nil {
					return err
				}
				s.logger.Info(ctx, "webhook delivery skipped",
					append(webhookAttrs, slog.String("reason", "no_active_endpoint"))...,
				)
				continue
			}
			if err := s.repo.RetryWebhook(ctx, event, err.Error()); err != nil {
				return err
			}
			s.logger.Info(ctx, retryMessage,
				append(webhookAttrs,
					slog.String("failure_type", "endpoint_lookup"),
					slog.Int64("duration_ms", time.Since(deliveryStarted).Milliseconds()),
				)...,
			)
			continue
		}
		timestamp := fmt.Sprint(time.Now().Unix())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.URL, strings.NewReader(event.PayloadJSON))
		if err != nil {
			if err := s.repo.RetryWebhook(ctx, event, err.Error()); err != nil {
				return err
			}
			s.logger.Info(ctx, retryMessage,
				append(webhookAttrs,
					slog.String("failure_type", "request_creation"),
					slog.Int64("duration_ms", time.Since(deliveryStarted).Milliseconds()),
				)...,
			)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Dodo-Event", event.EventType)
		req.Header.Set("X-Dodo-Timestamp", timestamp)
		req.Header.Set("X-Dodo-Signature", "sha256="+signWebhook(endpoint.Secret, timestamp, event.PayloadJSON))
		req.Header.Set("X-Dodo-Event-ID", event.ID)
		resp, err := s.webhookHTTP.Do(req)
		if err != nil {
			if err := s.repo.RetryWebhook(ctx, event, err.Error()); err != nil {
				return err
			}
			s.logger.Info(ctx, retryMessage,
				append(webhookAttrs,
					slog.String("failure_type", "transport"),
					slog.Int64("duration_ms", time.Since(deliveryStarted).Milliseconds()),
				)...,
			)
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if err := s.repo.MarkWebhookSent(ctx, event.ID); err != nil {
				return err
			}
			s.logger.Info(ctx, "webhook delivered",
				append(webhookAttrs,
					slog.Int("http_status", resp.StatusCode),
					slog.Int64("duration_ms", time.Since(deliveryStarted).Milliseconds()),
				)...,
			)
			continue
		}
		if err := s.repo.RetryWebhook(ctx, event, fmt.Sprintf("HTTP %d", resp.StatusCode)); err != nil {
			return err
		}
		s.logger.Info(ctx, retryMessage,
			append(webhookAttrs,
				slog.String("failure_type", "http_status"),
				slog.Int("http_status", resp.StatusCode),
				slog.Int64("duration_ms", time.Since(deliveryStarted).Milliseconds()),
			)...,
		)
	}
	return nil
}

func signWebhook(secret, timestamp, body string) string {
	h := hmac.New(sha256.New, []byte(secret))
	_, _ = h.Write([]byte(timestamp + "." + body))
	return hex.EncodeToString(h.Sum(nil))
}
