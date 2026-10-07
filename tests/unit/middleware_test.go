package tests

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dodo-payments/internal/logging"
	"dodo-payments/internal/middleware"
)

func TestRequestLoggingIncludesTraceAndIdempotencyKey(t *testing.T) {
	var output bytes.Buffer
	logger := logging.NewJSON(&output)
	handler := middleware.RequestLogging(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	request := httptest.NewRequest(http.MethodPost, "/invoices/inv-1/pay", nil)
	request.Header.Set("X-Trace-ID", "5ae92207-1d70-44ef-a7dd-4ee16d901d63")
	request.Header.Set("Idempotency-Key", "idem-test-42")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if got := recorder.Header().Get("X-Trace-ID"); got != request.Header.Get("X-Trace-ID") {
		t.Fatalf("response trace id = %q, want request trace id %q", got, request.Header.Get("X-Trace-ID"))
	}
	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &record); err != nil {
		t.Fatalf("decode structured request log: %v", err)
	}
	if record["trace_id"] != request.Header.Get("X-Trace-ID") {
		t.Errorf("trace_id = %v", record["trace_id"])
	}
	if record["idempotency_key"] != "idem-test-42" {
		t.Errorf("idempotency_key = %v", record["idempotency_key"])
	}
	source, ok := record["source"].(map[string]any)
	if !ok || source["file"] == "" || source["line"].(float64) <= 0 {
		t.Errorf("source does not identify request logger callsite: %#v", record["source"])
	}
}
