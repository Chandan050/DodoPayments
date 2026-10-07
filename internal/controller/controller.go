package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strings"

	"dodo-payments/internal/apperr"
	"dodo-payments/internal/logging"
	"dodo-payments/internal/middleware"
	"dodo-payments/internal/model"
	"dodo-payments/internal/service"
)

type Controller struct {
	service  *service.Service
	apiKeyMW *middleware.APIKey
	logger   *logging.Logger
}

func New(svc *service.Service, auth *middleware.APIKey, logger *logging.Logger) *Controller {
	return &Controller{service: svc, apiKeyMW: auth, logger: logger}
}

func (c *Controller) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", withSource(c.health, reflect.ValueOf((*Controller).health).Pointer()))
	mux.Handle("/customers", c.apiKeyMW.Wrap(http.HandlerFunc(withSource(c.customers, reflect.ValueOf((*Controller).customers).Pointer()))))
	mux.Handle("/invoices", c.apiKeyMW.Wrap(http.HandlerFunc(withSource(c.invoices, reflect.ValueOf((*Controller).invoices).Pointer()))))
	mux.Handle("/invoices/", c.apiKeyMW.Wrap(http.HandlerFunc(withSource(c.invoiceByID, reflect.ValueOf((*Controller).invoiceByID).Pointer()))))
	mux.Handle("/webhooks/endpoints", c.apiKeyMW.Wrap(http.HandlerFunc(withSource(c.webhookEndpoints, reflect.ValueOf((*Controller).webhookEndpoints).Pointer()))))
	return mux
}

func withSource(handler http.HandlerFunc, pc uintptr) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		middleware.SetRequestSource(r.Context(), pc)
		handler(w, r)
	}
}

func (c *Controller) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is supported")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (c *Controller) customers(w http.ResponseWriter, r *http.Request) {
	business, ok := middleware.BusinessFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "business context is missing")
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, err := c.service.ListCustomers(r.Context(), business.ID)
		if err != nil {
			c.internalError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	case http.MethodPost:
		var request struct {
			Name  string `json:"name"`
			Email string `json:"email"`
		}
		if err := decodeJSON(r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body")
			return
		}
		if strings.TrimSpace(request.Name) == "" || strings.TrimSpace(request.Email) == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "name and email are required")
			return
		}
		customer, err := c.service.CreateCustomer(r.Context(), business.ID, request.Name, request.Email)
		if err != nil {
			c.internalError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, customer)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET and POST are supported")
	}
}

func (c *Controller) invoices(w http.ResponseWriter, r *http.Request) {
	business, ok := middleware.BusinessFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "business context is missing")
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, err := c.service.ListInvoices(r.Context(), business.ID, r.URL.Query().Get("state"))
		if err != nil {
			c.internalError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	case http.MethodPost:
		var request struct {
			CustomerID string           `json:"customer_id"`
			Currency   string           `json:"currency"`
			DueDate    string           `json:"due_date"`
			LineItems  []model.LineItem `json:"line_items"`
		}
		if err := decodeJSON(r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body")
			return
		}
		if request.Currency == "" {
			request.Currency = "USD"
		}
		invoice, err := c.service.CreateInvoice(r.Context(), business.ID, request.CustomerID, request.Currency, request.DueDate, request.LineItems)
		if err != nil {
			c.handleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, invoice)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET and POST are supported")
	}
}

func (c *Controller) invoiceByID(w http.ResponseWriter, r *http.Request) {
	business, ok := middleware.BusinessFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "business context is missing")
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/invoices/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "invoice id is required")
		return
	}
	invoiceID := parts[0]
	if r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "payment" {
		attempt, err := c.service.GetLatestPaymentAttempt(r.Context(), business.ID, invoiceID)
		if err != nil {
			c.handleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, attempt)
		return
	}
	if r.Method == http.MethodGet && len(parts) == 1 {
		invoice, err := c.service.GetInvoice(r.Context(), business.ID, invoiceID)
		if err != nil {
			c.handleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, invoice)
		return
	}
	if r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "pay" {
		c.payInvoice(w, r, business.ID, invoiceID)
		return
	}
	if r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "void" {
		if err := c.service.VoidInvoice(r.Context(), business.ID, invoiceID); err != nil {
			c.handleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"invoice_id": invoiceID, "state": "void"})
		return
	}
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "unsupported method or route")
}

func (c *Controller) payInvoice(w http.ResponseWriter, r *http.Request, businessID, invoiceID string) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeError(w, http.StatusBadRequest, "missing_idempotency_key", "Idempotency-Key header is required")
		return
	}
	var request struct {
		CardToken string `json:"card_token"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}
	if request.CardToken == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "card_token is required")
		return
	}
	response, err := c.service.PayInvoice(r.Context(), businessID, invoiceID, key, request.CardToken)
	if err != nil {
		c.handleError(w, r, err)
		return
	}
	writeRawJSON(w, response.Status, response.Body)
}

func (c *Controller) webhookEndpoints(w http.ResponseWriter, r *http.Request) {
	business, ok := middleware.BusinessFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "business context is missing")
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, err := c.service.ListWebhookEndpoints(r.Context(), business.ID)
		if err != nil {
			c.internalError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	case http.MethodPost:
		var request struct {
			URL    string `json:"url"`
			Secret string `json:"secret"`
		}
		if err := decodeJSON(r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body")
			return
		}
		endpoint, err := c.service.CreateWebhookEndpoint(r.Context(), business.ID, request.URL, request.Secret)
		if err != nil {
			c.handleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, endpoint)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET and POST are supported")
	}
}

func decodeJSON(r *http.Request, dest any) error {
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(body) > 1<<20 {
		return errors.New("request body is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(dest); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain exactly one JSON value")
	}
	return nil
}

func (c *Controller) handleError(w http.ResponseWriter, r *http.Request, err error) {
	appError := apperr.As(err)
	if appError.Status >= http.StatusInternalServerError {
		c.internalError(w, r, err)
		return
	}
	writeError(w, appError.Status, appError.Code, appError.Message)
}

func (c *Controller) internalError(w http.ResponseWriter, r *http.Request, err error) {
	appError := apperr.As(err)
	c.logger.Error(r.Context(), "request failed", err,
		slog.Int("http_status", appError.Status),
		slog.String("error_code", appError.Code),
	)
	writeError(w, appError.Status, appError.Code, appError.Message)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeRawJSON(w http.ResponseWriter, status int, payload []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
