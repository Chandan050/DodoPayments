package model

import (
	"encoding/json"
	"time"
)

type Business struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	APIKeyHash   string    `json:"-"`
	APIKeyPrefix string    `json:"api_key_prefix"`
	CreatedAt    time.Time `json:"created_at"`
}

type Customer struct {
	ID         string    `json:"id"`
	BusinessID string    `json:"business_id"`
	Name       string    `json:"name"`
	Email      string    `json:"email"`
	CreatedAt  time.Time `json:"created_at"`
}

type Invoice struct {
	ID         string    `json:"id"`
	BusinessID string    `json:"business_id"`
	CustomerID string    `json:"customer_id"`
	Currency   string    `json:"currency"`
	DueDate    string    `json:"due_date"`
	TotalCents int64     `json:"total_cents"`
	State      string    `json:"state"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type LineItem struct {
	ID              string `json:"id"`
	InvoiceID       string `json:"invoice_id,omitempty"`
	Description     string `json:"description"`
	Quantity        int64  `json:"quantity"`
	UnitAmountCents int64  `json:"unit_amount_cents"`
}

type PaymentAttempt struct {
	ID             string    `json:"id"`
	InvoiceID      string    `json:"invoice_id"`
	BusinessID     string    `json:"business_id"`
	Status         string    `json:"status"`
	AmountCents    int64     `json:"amount_cents"`
	Currency       string    `json:"currency"`
	ErrorCode      string    `json:"error_code,omitempty"`
	PSPReference   string    `json:"psp_reference,omitempty"`
	RetryCount     int       `json:"retry_count"`
	NextAttemptAt  time.Time `json:"next_attempt_at"`
	CreatedAt      time.Time `json:"created_at"`
	IdempotencyKey string    `json:"idempotency_key"`
	Token          string    `json:"-"`
	RequestHash    string    `json:"-"`
}

func SupportedCurrency(currency string) bool {
	switch currency {
	case "USD", "GBP", "INR", "EUR":
		return true
	default:
		return false
	}
}

type IdempotencyRecord struct {
	BusinessID     string          `json:"business_id"`
	InvoiceID      string          `json:"invoice_id"`
	IdempotencyKey string          `json:"idempotency_key"`
	RequestHash    string          `json:"request_hash"`
	Status         string          `json:"status"`
	ResponseCode   int             `json:"response_code"`
	ResponseBody   json.RawMessage `json:"response_body"`
}

type WebhookEndpoint struct {
	ID         string    `json:"id"`
	BusinessID string    `json:"business_id"`
	URL        string    `json:"url"`
	Secret     string    `json:"-"`
	Active     bool      `json:"active"`
	CreatedAt  time.Time `json:"created_at"`
}

type WebhookEvent struct {
	ID          string
	BusinessID  string
	EventType   string
	PayloadJSON string
	Attempts    int
}

func TransitionAllowed(from, to string) bool {
	switch from {
	case "draft":
		return to == "open" || to == "void"
	case "open":
		return to == "paid" || to == "void" || to == "uncollectible"
	default:
		return false
	}
}
