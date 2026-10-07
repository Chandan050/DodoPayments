package tests

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"dodo-payments/internal/controller"
	"dodo-payments/internal/logging"
	"dodo-payments/internal/middleware"
	"dodo-payments/internal/payment"
	"dodo-payments/internal/repository"
	"dodo-payments/internal/service"
	"dodo-payments/internal/storage"
)

type blockingPSP struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (p *blockingPSP) Charge(ctx context.Context, token string, amountCents int64, currency, key string) (payment.Result, error) {
	p.calls.Add(1)
	p.started <- struct{}{}
	select {
	case <-p.release:
		return payment.Result{Status: "succeeded", PSPReference: "psp-concurrency-test"}, nil
	case <-ctx.Done():
		return payment.Result{}, ctx.Err()
	}
}

func TestConcurrentPayRequestsChargeAtMostOnce(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run the PostgreSQL payment concurrency test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := storage.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.Migrate(db, "../migrations"); err != nil {
		t.Fatal(err)
	}

	businessID, customerID, invoiceID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	apiKey := "integration-" + uuid.NewString()
	keyHash := sha256.Sum256([]byte(apiKey))
	if _, err := db.ExecContext(ctx, `
		INSERT INTO businesses (id, name, api_key_prefix, api_key_hash, created_at)
		VALUES ($1, 'Concurrent Test', 'integration', $2, NOW())`,
		businessID, hex.EncodeToString(keyHash[:])); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.Exec(`DELETE FROM businesses WHERE id = $1`, businessID)
	}()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO customers (id, business_id, name, email, created_at)
		VALUES ($1, $2, 'Test Customer', 'test@example.invalid', NOW())`,
		customerID, businessID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO invoices (id, business_id, customer_id, due_date, total_cents, state, created_at, updated_at)
		VALUES ($1, $2, $3, CURRENT_DATE, 1200, 'open', NOW(), NOW())`,
		invoiceID, businessID, customerID); err != nil {
		t.Fatal(err)
	}

	psp := &blockingPSP{started: make(chan struct{}, 1), release: make(chan struct{})}
	repo := repository.New(db)
	logger := logging.NewJSON(io.Discard)
	svc := service.New(repo, psp, logger)
	auth := middleware.NewAPIKey(repo)
	server := httptest.NewServer(middleware.RequestLogging(logger, controller.New(svc, auth, logger).Routes()))
	defer server.Close()

	type paymentResult struct {
		status int
		err    error
	}
	firstResult := make(chan paymentResult, 1)
	go func() {
		status, err := postPayment(server.URL, apiKey, invoiceID, "concurrent-key-1")
		firstResult <- paymentResult{status: status, err: err}
	}()
	select {
	case <-psp.started:
	case <-time.After(3 * time.Second):
		t.Fatal("first request did not reach the PSP")
	}

	secondStatus, err := postPayment(server.URL, apiKey, invoiceID, "concurrent-key-2")
	if err != nil {
		t.Fatal(err)
	}
	if secondStatus != http.StatusConflict {
		t.Fatalf("second request status = %d, want %d", secondStatus, http.StatusConflict)
	}
	close(psp.release)
	first := <-firstResult
	if first.err != nil {
		t.Fatal(first.err)
	}
	if first.status != http.StatusOK {
		t.Fatalf("first request status = %d, want %d", first.status, http.StatusOK)
	}
	if got := psp.calls.Load(); got != 1 {
		t.Fatalf("PSP call count = %d, want 1", got)
	}

	var state string
	if err := db.QueryRowContext(ctx, `SELECT state FROM invoices WHERE id = $1`, invoiceID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "paid" {
		t.Fatalf("invoice state = %q, want paid", state)
	}
	var attempts int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM payment_attempts WHERE invoice_id = $1`, invoiceID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Fatalf("payment attempt count = %d, want 1", attempts)
	}
}

func postPayment(serverURL, apiKey, invoiceID, idempotencyKey string) (int, error) {
	request, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/invoices/%s/pay", serverURL, invoiceID),
		strings.NewReader(`{"card_token":"tok_success"}`))
	if err != nil {
		return 0, err
	}
	request.Header.Set("X-API-Key", apiKey)
	request.Header.Set("Idempotency-Key", idempotencyKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	return response.StatusCode, nil
}
