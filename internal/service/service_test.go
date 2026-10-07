package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"dodo-payments/internal/apperr"
	"dodo-payments/internal/model"
)

func TestCreateInvoiceRejectsInvalidMoney(t *testing.T) {
	svc := &Service{}
	tests := []struct {
		name  string
		items []model.LineItem
		want  string
	}{
		{
			name:  "nonpositive quantity",
			items: []model.LineItem{{Description: "item", Quantity: 0, UnitAmountCents: 100}},
			want:  "quantity > 0",
		},
		{
			name:  "multiplication overflow",
			items: []model.LineItem{{Description: "item", Quantity: math.MaxInt64, UnitAmountCents: 2}},
			want:  "supported range",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := svc.CreateInvoice(context.Background(), "business", "customer", "USD", "2026-10-30", test.items)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("CreateInvoice error = %v, want text %q", err, test.want)
			}
		})
	}
}

func TestReplayIdempotencyValidatesRequestHash(t *testing.T) {
	record := &model.IdempotencyRecord{
		RequestHash:  "body-hash",
		ResponseCode: 202,
		ResponseBody: json.RawMessage(`{"payment_status":"pending"}`),
	}
	response, err := replayIdempotency(record, "body-hash")
	if err != nil {
		t.Fatalf("replayIdempotency() error = %v", err)
	}
	if response.Status != 202 || string(response.Body) != string(record.ResponseBody) {
		t.Fatalf("unexpected replay response: %#v", response)
	}
	if _, err := replayIdempotency(record, "different-body"); err != apperr.ErrIdempotencyConflict {
		t.Fatalf("replay with a different body error = %v, want conflict", err)
	}
}

func TestCreateInvoiceRejectsMissingAndMalformedInputs(t *testing.T) {
	svc := &Service{}
	tests := []struct {
		name     string
		customer string
		dueDate  string
		items    []model.LineItem
		wantText string
	}{
		{name: "missing customer", customer: "", dueDate: "2026-10-30", items: []model.LineItem{{Description: "item", Quantity: 1, UnitAmountCents: 100}}, wantText: "customer_id, currency, due_date, and line_items are required"},
		{name: "missing due date", customer: "customer-1", dueDate: "", items: []model.LineItem{{Description: "item", Quantity: 1, UnitAmountCents: 100}}, wantText: "customer_id, currency, due_date, and line_items are required"},
		{name: "missing items", customer: "customer-1", dueDate: "2026-10-30", items: nil, wantText: "customer_id, currency, due_date, and line_items are required"},
		{name: "invalid date format", customer: "customer-1", dueDate: "10/30/2026", items: []model.LineItem{{Description: "item", Quantity: 1, UnitAmountCents: 100}}, wantText: "due_date must be YYYY-MM-DD"},
		{name: "empty description", customer: "customer-1", dueDate: "2026-10-30", items: []model.LineItem{{Description: " ", Quantity: 1, UnitAmountCents: 100}}, wantText: "description, quantity > 0, and unit_amount_cents > 0"},
		{name: "nonpositive unit amount", customer: "customer-1", dueDate: "2026-10-30", items: []model.LineItem{{Description: "item", Quantity: 2, UnitAmountCents: 0}}, wantText: "description, quantity > 0, and unit_amount_cents > 0"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := svc.CreateInvoice(context.Background(), "business-1", test.customer, "USD", test.dueDate, test.items)
			if err == nil || !strings.Contains(err.Error(), test.wantText) {
				t.Fatalf("CreateInvoice error = %v, want text %q", err, test.wantText)
			}
		})
	}
}

func TestCreateInvoiceRejectsUnsupportedCurrency(t *testing.T) {
	svc := &Service{}
	_, err := svc.CreateInvoice(context.Background(), "business-1", "customer-1", "CAD", "2026-10-30",
		[]model.LineItem{{Description: "item", Quantity: 1, UnitAmountCents: 100}})
	if err == nil || !strings.Contains(err.Error(), "currency must be one of USD, GBP, INR, or EUR") {
		t.Fatalf("CreateInvoice() error = %v, want unsupported currency error", err)
	}
}

func TestReplayIdempotencyDefaultsEmptyBodyToObject(t *testing.T) {
	record := &model.IdempotencyRecord{
		RequestHash:  "body-hash",
		ResponseCode: 202,
	}
	response, err := replayIdempotency(record, "body-hash")
	if err != nil {
		t.Fatalf("replayIdempotency() error = %v", err)
	}
	if response.Status != 202 {
		t.Fatalf("response status = %d, want 202", response.Status)
	}
	if string(response.Body) != `{}` {
		t.Fatalf("response body = %s, want {}", string(response.Body))
	}
}

func TestCreateWebhookEndpointRejectsBlankInput(t *testing.T) {
	svc := &Service{}
	_, err := svc.CreateWebhookEndpoint(context.Background(), "business-1", "", "secret")
	if err == nil || !strings.Contains(err.Error(), "url and secret are required") {
		t.Fatalf("CreateWebhookEndpoint empty URL error = %v, want required validation", err)
	}

	_, err = svc.CreateWebhookEndpoint(context.Background(), "business-1", "https://example.com/webhook", "")
	if err == nil || !strings.Contains(err.Error(), "url and secret are required") {
		t.Fatalf("CreateWebhookEndpoint empty secret error = %v, want required validation", err)
	}
}

func TestSignWebhookUsesHMACWithTimestampAndBody(t *testing.T) {
	const secret = "super-secret"
	const timestamp = "1729876543"
	const body = `{"event":"paid"}`

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "." + body))
	want := hex.EncodeToString(mac.Sum(nil))

	if got := signWebhook(secret, timestamp, body); got != want {
		t.Fatalf("signWebhook() = %q, want %q", got, want)
	}
}
