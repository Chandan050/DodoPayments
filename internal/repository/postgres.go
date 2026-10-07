package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"dodo-payments/internal/apperr"
	"dodo-payments/internal/model"
)

type Repository struct {
	db *sql.DB
}

func New(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Authenticate(ctx context.Context, keyHash string) (*model.Business, error) {
	var business model.Business
	err := r.db.QueryRowContext(ctx, `
		SELECT id, name, api_key_prefix, api_key_hash, created_at
		FROM businesses
		WHERE api_key_hash = $1 AND revoked_at IS NULL
		LIMIT 1`, keyHash).Scan(
		&business.ID, &business.Name, &business.APIKeyPrefix,
		&business.APIKeyHash, &business.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperr.ErrNotFound
	}
	return &business, err
}

func (r *Repository) ListCustomers(ctx context.Context, businessID string) ([]model.Customer, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, business_id, name, email, created_at
		FROM customers WHERE business_id = $1 ORDER BY created_at DESC`, businessID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	customers := make([]model.Customer, 0)
	for rows.Next() {
		var customer model.Customer
		if err := rows.Scan(&customer.ID, &customer.BusinessID, &customer.Name, &customer.Email, &customer.CreatedAt); err != nil {
			return nil, err
		}
		customers = append(customers, customer)
	}
	return customers, rows.Err()
}

func (r *Repository) CreateCustomer(ctx context.Context, businessID, name, email string) (model.Customer, error) {
	customer := model.Customer{ID: uuid.NewString(), BusinessID: businessID, Name: name, Email: email}
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO customers (id, business_id, name, email, created_at)
		VALUES ($1, $2, $3, $4, NOW())
		RETURNING created_at`, customer.ID, customer.BusinessID, customer.Name, customer.Email,
	).Scan(&customer.CreatedAt)
	return customer, err
}

func (r *Repository) CreateInvoice(ctx context.Context, businessID, customerID, currency, dueDate string, total int64, items []model.LineItem) (model.Invoice, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Invoice{}, err
	}
	defer tx.Rollback()

	var exists bool
	err = tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM customers WHERE id = $1 AND business_id = $2)`,
		customerID, businessID).Scan(&exists)
	if err != nil {
		return model.Invoice{}, err
	}
	if !exists {
		return model.Invoice{}, apperr.ErrNotFound
	}

	invoice := model.Invoice{
		ID: uuid.NewString(), BusinessID: businessID, CustomerID: customerID,
		Currency: currency, DueDate: dueDate, TotalCents: total, State: "open",
	}
	err = tx.QueryRowContext(ctx, `
		INSERT INTO invoices (id, business_id, customer_id, currency, due_date, total_cents, state, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'open', NOW(), NOW())
		RETURNING created_at, updated_at`,
		invoice.ID, invoice.BusinessID, invoice.CustomerID, invoice.Currency, invoice.DueDate, invoice.TotalCents,
	).Scan(&invoice.CreatedAt, &invoice.UpdatedAt)
	if err != nil {
		return model.Invoice{}, err
	}
	for _, item := range items {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO invoice_line_items (id, invoice_id, description, quantity, unit_amount_cents)
			VALUES ($1, $2, $3, $4, $5)`,
			uuid.NewString(), invoice.ID, item.Description, item.Quantity, item.UnitAmountCents); err != nil {
			return model.Invoice{}, err
		}
	}
	if err := insertWebhookEvent(ctx, tx, businessID, "invoice.created", map[string]any{
		"invoice_id": invoice.ID, "customer_id": customerID, "total_cents": total,
		"state": invoice.State, "due_date": dueDate, "currency": currency,
	}); err != nil {
		return model.Invoice{}, err
	}
	return invoice, tx.Commit()
}

func (r *Repository) ListInvoices(ctx context.Context, businessID, state string) ([]model.Invoice, error) {
	query := `SELECT id, business_id, customer_id, currency, due_date, total_cents, state, created_at, updated_at
		FROM invoices WHERE business_id = $1`
	args := []any{businessID}
	if state != "" {
		query += ` AND state = $2`
		args = append(args, state)
	}
	query += ` ORDER BY created_at DESC`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	invoices := make([]model.Invoice, 0)
	for rows.Next() {
		var invoice model.Invoice
		if err := rows.Scan(&invoice.ID, &invoice.BusinessID, &invoice.CustomerID, &invoice.Currency, &invoice.DueDate,
			&invoice.TotalCents, &invoice.State, &invoice.CreatedAt, &invoice.UpdatedAt); err != nil {
			return nil, err
		}
		invoices = append(invoices, invoice)
	}
	return invoices, rows.Err()
}

func (r *Repository) GetInvoice(ctx context.Context, businessID, invoiceID string) (model.Invoice, error) {
	var invoice model.Invoice
	err := r.db.QueryRowContext(ctx, `
		SELECT id, business_id, customer_id, currency, due_date, total_cents, state, created_at, updated_at
		FROM invoices WHERE id = $1 AND business_id = $2`, invoiceID, businessID).Scan(
		&invoice.ID, &invoice.BusinessID, &invoice.CustomerID, &invoice.Currency, &invoice.DueDate,
		&invoice.TotalCents, &invoice.State, &invoice.CreatedAt, &invoice.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Invoice{}, apperr.ErrNotFound
	}
	return invoice, err
}

func (r *Repository) VoidInvoice(ctx context.Context, businessID, invoiceID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var state string
	if err := tx.QueryRowContext(ctx, `
		SELECT state FROM invoices WHERE id = $1 AND business_id = $2 FOR UPDATE`,
		invoiceID, businessID).Scan(&state); errors.Is(err, sql.ErrNoRows) {
		return apperr.ErrNotFound
	} else if err != nil {
		return err
	}
	if !model.TransitionAllowed(state, "void") {
		return apperr.ErrInvalidTransition
	}
	var paymentInProgress bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM payment_attempts
			WHERE invoice_id = $1 AND business_id = $2 AND status IN ('pending', 'processing')
		)`, invoiceID, businessID).Scan(&paymentInProgress); err != nil {
		return err
	}
	if paymentInProgress {
		return apperr.ErrPaymentInProgress
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE invoices SET state = 'void', updated_at = NOW()
		WHERE id = $1 AND business_id = $2`, invoiceID, businessID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repository) FindIdempotency(ctx context.Context, businessID, invoiceID, key string) (*model.IdempotencyRecord, error) {
	var record model.IdempotencyRecord
	var raw []byte
	err := r.db.QueryRowContext(ctx, `
		SELECT business_id, invoice_id, idempotency_key, request_hash, status, response_code, response_body
		FROM idempotency_keys WHERE business_id = $1 AND invoice_id = $2 AND idempotency_key = $3`,
		businessID, invoiceID, key).Scan(
		&record.BusinessID, &record.InvoiceID, &record.IdempotencyKey, &record.RequestHash,
		&record.Status, &record.ResponseCode, &raw,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	record.ResponseBody = raw
	return &record, err
}

func (r *Repository) BeginPayment(ctx context.Context, businessID, invoiceID, key, token, requestHash string) (string, int64, string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, "", err
	}
	defer tx.Rollback()

	var state string
	var total int64
	var currency string
	err = tx.QueryRowContext(ctx, `
		SELECT state, total_cents, currency FROM invoices
		WHERE id = $1 AND business_id = $2 FOR UPDATE`, invoiceID, businessID).Scan(&state, &total, &currency)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, "", apperr.ErrNotFound
	} else if err != nil {
		return "", 0, "", err
	}
	if state != "open" {
		return "", 0, "", apperr.ErrInvalidTransition
	}

	var active bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM payment_attempts WHERE invoice_id = $1 AND status IN ('pending', 'processing'))`,
		invoiceID).Scan(&active); err != nil {
		return "", 0, "", err
	}
	if active {
		return "", 0, "", apperr.ErrPaymentInProgress
	}

	attemptID := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO payment_attempts (id, invoice_id, business_id, idempotency_key, token, status, amount_cents, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'processing', $6, NOW(), NOW())`,
		attemptID, invoiceID, businessID, key, token, total); err != nil {
		return "", 0, "", err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO idempotency_keys (business_id, invoice_id, idempotency_key, request_hash, status, response_code, response_body, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'processing', 202, '{}', NOW(), NOW())`,
		businessID, invoiceID, key, requestHash); err != nil {
		return "", 0, "", err
	}
	if err := tx.Commit(); err != nil {
		return "", 0, "", err
	}
	return attemptID, total, currency, nil
}

func (r *Repository) FinishPayment(ctx context.Context, businessID, invoiceID, attemptID, key, state, paymentStatus, code, pspRef string, amountCents int64, currency string, statusCode int) ([]byte, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if paymentStatus == "succeeded" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE payment_attempts SET status = 'succeeded', psp_reference = $1, error_code = NULL, updated_at = NOW()
			WHERE id = $2 AND business_id = $3`, pspRef, attemptID, businessID); err != nil {
			return nil, err
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE invoices SET state = 'paid', updated_at = NOW()
			WHERE id = $1 AND business_id = $2 AND state = 'open'`, invoiceID, businessID)
		if err != nil {
			return nil, err
		}
		if count, _ := result.RowsAffected(); count != 1 {
			return nil, apperr.ErrInvalidTransition
		}
	} else {
		if _, err := tx.ExecContext(ctx, `
			UPDATE payment_attempts SET status = 'failed', error_code = $1, updated_at = NOW()
			WHERE id = $2 AND business_id = $3`, code, attemptID, businessID); err != nil {
			return nil, err
		}
	}

	body, err := json.Marshal(map[string]any{
		"invoice_id": invoiceID, "state": state, "payment_status": paymentStatus,
		"error_code": code, "psp_reference": pspRef, "amount_cents": amountCents, "currency": currency,
	})
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE idempotency_keys SET status = 'completed', response_code = $1,
		response_body = $2, updated_at = NOW()
		WHERE business_id = $3 AND invoice_id = $4 AND idempotency_key = $5`,
		statusCode, body, businessID, invoiceID, key); err != nil {
		return nil, err
	}
	event := "invoice.payment_failed"
	payload := map[string]any{
		"invoice_id": invoiceID, "business_id": businessID, "code": code,
		"amount_cents": amountCents, "currency": currency,
	}
	if paymentStatus == "succeeded" {
		event = "invoice.paid"
		payload = map[string]any{
			"invoice_id": invoiceID, "business_id": businessID, "psp_reference": pspRef,
			"amount_cents": amountCents, "currency": currency,
		}
	}
	if err := insertWebhookEvent(ctx, tx, businessID, event, payload); err != nil {
		return nil, err
	}
	return body, tx.Commit()
}

func (r *Repository) MarkPaymentPending(ctx context.Context, invoiceID, businessID, attemptID, key string, amountCents int64, currency string, cause error) ([]byte, error) {
	code := "network_error"
	if cause != nil {
		code = "network_error"
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE payment_attempts
		SET status = 'pending', error_code = $1, retry_count = retry_count + 1,
			next_attempt_at = NOW() + (
				CASE retry_count
					WHEN 0 THEN 5
					WHEN 1 THEN 30
					WHEN 2 THEN 120
					WHEN 3 THEN 600
					ELSE 3600
				END * INTERVAL '1 second'
			),
			updated_at = NOW()
		WHERE id = $2 AND business_id = $3`, code, attemptID, businessID); err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]any{
		"invoice_id": invoiceID, "payment_status": "pending",
		"amount_cents": amountCents, "currency": currency,
		"message": "payment result is unknown; retry with the same idempotency key",
	})
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE idempotency_keys SET status = 'pending', response_code = 202, response_body = $1, updated_at = NOW()
		WHERE business_id = $2 AND invoice_id = $3 AND idempotency_key = $4`,
		body, businessID, invoiceID, key); err != nil {
		return nil, err
	}
	return body, tx.Commit()
}

func (r *Repository) LatestPaymentAttempt(ctx context.Context, businessID, invoiceID string) (model.PaymentAttempt, error) {
	var attempt model.PaymentAttempt
	err := r.db.QueryRowContext(ctx, `
		SELECT id, invoice_id, business_id, status, amount_cents, currency, error_code, psp_reference,
			retry_count, next_attempt_at, created_at, idempotency_key
		FROM payment_attempts
		WHERE business_id = $1 AND invoice_id = $2
		ORDER BY created_at DESC LIMIT 1`, businessID, invoiceID).Scan(
		&attempt.ID, &attempt.InvoiceID, &attempt.BusinessID, &attempt.Status, &attempt.AmountCents,
		&attempt.Currency, &attempt.ErrorCode, &attempt.PSPReference, &attempt.RetryCount,
		&attempt.NextAttemptAt, &attempt.CreatedAt, &attempt.IdempotencyKey,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.PaymentAttempt{}, apperr.ErrNotFound
	}
	return attempt, err
}

func (r *Repository) ClaimPendingPayments(ctx context.Context, limit int) ([]model.PaymentAttempt, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT pa.id, pa.invoice_id, pa.business_id, pa.idempotency_key, pa.token, pa.amount_cents,
			i.currency, ik.request_hash
		FROM payment_attempts pa
		JOIN invoices i ON i.id = pa.invoice_id AND i.business_id = pa.business_id
		JOIN idempotency_keys ik ON ik.business_id = pa.business_id
			AND ik.invoice_id = pa.invoice_id AND ik.idempotency_key = pa.idempotency_key
		WHERE i.state = 'open' AND (
			(pa.status = 'pending' AND pa.next_attempt_at <= NOW()) OR
			(pa.status = 'processing' AND pa.updated_at < NOW() - INTERVAL '5 minutes')
		)
		ORDER BY pa.created_at
		FOR UPDATE OF pa SKIP LOCKED
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	attempts := make([]model.PaymentAttempt, 0)
	for rows.Next() {
		var attempt model.PaymentAttempt
		if err := rows.Scan(&attempt.ID, &attempt.InvoiceID, &attempt.BusinessID,
			&attempt.IdempotencyKey, &attempt.Token, &attempt.AmountCents, &attempt.Currency, &attempt.RequestHash); err != nil {
			rows.Close()
			return nil, err
		}
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, attempt := range attempts {
		if _, err := tx.ExecContext(ctx, `
			UPDATE payment_attempts SET status = 'processing', updated_at = NOW() WHERE id = $1`, attempt.ID); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE idempotency_keys SET status = 'processing', updated_at = NOW()
			WHERE business_id = $1 AND invoice_id = $2 AND idempotency_key = $3`,
			attempt.BusinessID, attempt.InvoiceID, attempt.IdempotencyKey); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return attempts, nil
}

func (r *Repository) CreateWebhookEndpoint(ctx context.Context, businessID, url, secret string) (model.WebhookEndpoint, error) {
	endpoint := model.WebhookEndpoint{ID: uuid.NewString(), BusinessID: businessID, URL: url, Secret: secret, Active: true}
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO webhook_endpoints (id, business_id, url, secret, active, created_at)
		VALUES ($1, $2, $3, $4, true, NOW()) RETURNING created_at`,
		endpoint.ID, businessID, url, secret).Scan(&endpoint.CreatedAt)
	return endpoint, err
}

func (r *Repository) ListWebhookEndpoints(ctx context.Context, businessID string) ([]model.WebhookEndpoint, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, business_id, url, secret, active, created_at
		FROM webhook_endpoints WHERE business_id = $1 ORDER BY created_at DESC`, businessID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	endpoints := make([]model.WebhookEndpoint, 0)
	for rows.Next() {
		var endpoint model.WebhookEndpoint
		if err := rows.Scan(&endpoint.ID, &endpoint.BusinessID, &endpoint.URL, &endpoint.Secret, &endpoint.Active, &endpoint.CreatedAt); err != nil {
			return nil, err
		}
		endpoints = append(endpoints, endpoint)
	}
	return endpoints, rows.Err()
}

func (r *Repository) ClaimWebhookEvents(ctx context.Context, limit int) ([]model.WebhookEvent, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE webhook_events SET status = 'pending', updated_at = NOW()
		WHERE status = 'processing' AND updated_at < NOW() - INTERVAL '5 minutes'`); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, business_id, event_type, payload_json::text, attempts
		FROM webhook_events
		WHERE status = 'pending' AND next_attempt_at <= NOW()
		ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	events := make([]model.WebhookEvent, 0)
	for rows.Next() {
		var event model.WebhookEvent
		if err := rows.Scan(&event.ID, &event.BusinessID, &event.EventType, &event.PayloadJSON, &event.Attempts); err != nil {
			rows.Close()
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, event := range events {
		if _, err := tx.ExecContext(ctx, `UPDATE webhook_events SET status = 'processing', updated_at = NOW() WHERE id = $1`, event.ID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return events, nil
}

func (r *Repository) WebhookEndpointForBusiness(ctx context.Context, businessID string) (model.WebhookEndpoint, error) {
	var endpoint model.WebhookEndpoint
	err := r.db.QueryRowContext(ctx, `
		SELECT id, business_id, url, secret, active, created_at
		FROM webhook_endpoints WHERE business_id = $1 AND active = true
		ORDER BY created_at DESC LIMIT 1`, businessID).Scan(
		&endpoint.ID, &endpoint.BusinessID, &endpoint.URL, &endpoint.Secret, &endpoint.Active, &endpoint.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.WebhookEndpoint{}, apperr.ErrNotFound
	}
	return endpoint, err
}

func (r *Repository) MarkWebhookSent(ctx context.Context, eventID string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE webhook_events SET status = 'sent', attempts = attempts + 1, updated_at = NOW()
		WHERE id = $1`, eventID)
	return err
}

func (r *Repository) RetryWebhook(ctx context.Context, event model.WebhookEvent, lastError string) error {
	attempts := event.Attempts + 1
	if attempts >= 5 {
		_, err := r.db.ExecContext(ctx, `
			UPDATE webhook_events SET status = 'failed', attempts = $1, last_error = $2, updated_at = NOW()
			WHERE id = $3`, attempts, lastError, event.ID)
		return err
	}
	delay := []int{60, 300, 1800, 7200}[attempts-1]
	_, err := r.db.ExecContext(ctx, `
		UPDATE webhook_events SET status = 'pending', attempts = $1,
		next_attempt_at = NOW() + ($2 * INTERVAL '1 second'), last_error = $3, updated_at = NOW()
		WHERE id = $4`, attempts, delay, lastError, event.ID)
	return err
}

func insertWebhookEvent(ctx context.Context, tx *sql.Tx, businessID, eventType string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO webhook_events (id, business_id, event_type, payload_json, status, attempts, next_attempt_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4::jsonb, 'pending', 0, NOW(), NOW(), NOW())`,
		uuid.NewString(), businessID, eventType, string(data))
	return err
}
