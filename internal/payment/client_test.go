package payment

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dodo-payments/internal/logging"
)

func TestClientChargeForwardsIdempotencyKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/payments/charge" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "attempt-1" {
			t.Errorf("Idempotency-Key = %q, want attempt-1", got)
		}
		if got := r.Header.Get("X-Trace-ID"); got != "trace-client-test" {
			t.Errorf("X-Trace-ID = %q, want trace-client-test", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if payload["token"] != "tok_success" {
			t.Errorf("token = %q, want tok_success", payload["token"])
		}
		if payload["amount_cents"] != float64(1500) || payload["currency"] != "GBP" {
			t.Errorf("amount/currency = %v/%v, want 1500/GBP", payload["amount_cents"], payload["currency"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"succeeded","psp_ref":"psp-1"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, 0)
	ctx := logging.WithTraceID(context.Background(), "trace-client-test")
	result, err := client.Charge(ctx, "tok_success", 1500, "GBP", "attempt-1")
	if err != nil {
		t.Fatalf("Charge() error = %v", err)
	}
	if result.Status != "succeeded" || result.PSPReference != "psp-1" {
		t.Fatalf("unexpected PSP result: %#v", result)
	}
}
