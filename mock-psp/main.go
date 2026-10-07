package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
	AddSource: true,
	Level:     slog.LevelInfo,
	ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
		if len(groups) == 0 && attr.Key == slog.TimeKey {
			attr.Key = "timestamp"
		}
		return attr
	},
}))

type pspResponse struct {
	Status     string `json:"status"`
	PSPRef     string `json:"psp_ref,omitempty"`
	Code       string `json:"code,omitempty"`
	Message    string `json:"message,omitempty"`
	RetryAfter int    `json:"retry_after,omitempty"`
}

type idempotencyEntry struct {
	Token       string
	AmountCents int64
	Currency    string
	Body        []byte
	Code        int
	Done        chan struct{}
}

var (
	mu        sync.Mutex
	responses = map[string]*idempotencyEntry{}
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/payments/charge", handleCharge)
	logger.Info("mock PSP listening", slog.String("address", ":8081"))
	if err := http.ListenAndServe(":8081", mux); err != nil {
		logger.Error("mock PSP stopped", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func handleCharge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var payload struct {
		Token       string `json:"token"`
		AmountCents int64  `json:"amount_cents"`
		Currency    string `json:"currency"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if payload.Token == "" || payload.AmountCents <= 0 || (payload.Currency != "USD" && payload.Currency != "GBP" && payload.Currency != "INR" && payload.Currency != "EUR") {
		http.Error(w, "token, positive amount_cents, and a supported currency are required", http.StatusBadRequest)
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	traceID := strings.TrimSpace(r.Header.Get("X-Trace-ID"))
	if traceID == "" {
		traceID = randomID()
	}
	r.Header.Set("X-Trace-ID", traceID)
	w.Header().Set("X-Trace-ID", traceID)
	attrs := []any{slog.String("trace_id", traceID)}
	if idempotencyKey != "" {
		attrs = append(attrs, slog.String("idempotency_key", idempotencyKey))
	}
	logger.Info("mock PSP charge requested", attrs...)
	var current *idempotencyEntry
	if idempotencyKey != "" {
		mu.Lock()
		if existing, ok := responses[idempotencyKey]; ok {
			if existing.Token != payload.Token || existing.AmountCents != payload.AmountCents || existing.Currency != payload.Currency {
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "idempotency_conflict", "message": "same key used with different request"}})
				return
			}
			done := existing.Done
			mu.Unlock()
			select {
			case <-done:
			case <-r.Context().Done():
				return
			}
			mu.Lock()
			existing, ok = responses[idempotencyKey]
			mu.Unlock()
			if !ok {
				http.Error(w, "previous attempt has no cached result; retry", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(existing.Code)
			_, _ = w.Write(existing.Body)
			return
		}
		current = &idempotencyEntry{
			Token: payload.Token, AmountCents: payload.AmountCents, Currency: payload.Currency,
			Done: make(chan struct{}),
		}
		responses[idempotencyKey] = current
		mu.Unlock()
	}

	switch payload.Token {
	case "tok_success":
		time.Sleep(100 * time.Millisecond)
		writeResponse(w, pspResponse{Status: "succeeded", PSPRef: randomID()}, idempotencyKey, current)
	case "tok_insufficient_funds":
		time.Sleep(100 * time.Millisecond)
		writeResponse(w, pspResponse{Status: "failed", Code: "insufficient_funds"}, idempotencyKey, current)
	case "tok_card_declined":
		time.Sleep(100 * time.Millisecond)
		writeResponse(w, pspResponse{Status: "failed", Code: "card_declined"}, idempotencyKey, current)
	case "tok_timeout":
		time.Sleep(30 * time.Second)
		writeResponse(w, pspResponse{Status: "succeeded", PSPRef: randomID()}, idempotencyKey, current)
	case "tok_network_error":
		if os.Getenv("MOCK_PSP_FORCE_FAIL") == "close" {
			http.Error(w, "network error", http.StatusInternalServerError)
			clearPending(idempotencyKey, current)
			return
		}
		http.Error(w, "simulated network error", http.StatusInternalServerError)
		clearPending(idempotencyKey, current)
	case "tok_rate_limited":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "rate_limited"})
		clearPending(idempotencyKey, current)
	default:
		http.Error(w, "unsupported token", http.StatusBadRequest)
		clearPending(idempotencyKey, current)
	}
}

func writeResponse(w http.ResponseWriter, response pspResponse, idempotencyKey string, current *idempotencyEntry) {
	body, err := json.Marshal(response)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if idempotencyKey != "" {
		mu.Lock()
		current.Body = body
		current.Code = http.StatusOK
		close(current.Done)
		mu.Unlock()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func clearPending(idempotencyKey string, current *idempotencyEntry) {
	if idempotencyKey == "" || current == nil {
		return
	}
	mu.Lock()
	if responses[idempotencyKey] == current {
		delete(responses, idempotencyKey)
		close(current.Done)
	}
	mu.Unlock()
}

func randomID() string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d-%d", time.Now().UnixNano(), rand.Int())))
	return strings.ToLower(hex.EncodeToString(sum[:])[:32])
}
