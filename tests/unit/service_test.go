package tests

import (
	"context"
	"math"
	"strings"
	"testing"

	"dodo-payments/internal/model"
	"dodo-payments/internal/service"
)

func TestCreateInvoiceRejectsInvalidMoney(t *testing.T) {
	svc := &service.Service{}
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

func TestCreateInvoiceRejectsMissingAndMalformedInputs(t *testing.T) {
	svc := &service.Service{}
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

func TestCreateWebhookEndpointRejectsBlankInput(t *testing.T) {
	svc := &service.Service{}
	_, err := svc.CreateWebhookEndpoint(context.Background(), "business-1", "", "secret")
	if err == nil || !strings.Contains(err.Error(), "url and secret are required") {
		t.Fatalf("CreateWebhookEndpoint empty URL error = %v, want required validation", err)
	}

	_, err = svc.CreateWebhookEndpoint(context.Background(), "business-1", "https://example.com/webhook", "")
	if err == nil || !strings.Contains(err.Error(), "url and secret are required") {
		t.Fatalf("CreateWebhookEndpoint empty secret error = %v, want required validation", err)
	}
}
