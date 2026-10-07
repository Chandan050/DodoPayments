package middleware

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"dodo-payments/internal/logging"
)

func TestRequestLoggingIncludesTraceAndIdempotencyKey(t *testing.T) {
	var output bytes.Buffer
	logger := logging.NewJSON(&output)
	handler := RequestLogging(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	if !ok || !strings.Contains(source["file"].(string), "request_logging.go") {
		t.Errorf("source does not identify request logger callsite: %#v", record["source"])
	}
}

func TestRequestLoggingUsesHandlerSource(t *testing.T) {
	var output bytes.Buffer
	logger := logging.NewJSON(&output)
	handler := RequestLogging(logger, http.HandlerFunc(requestLoggingSourceHandler))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/test", nil))

	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &record); err != nil {
		t.Fatalf("decode structured request log: %v", err)
	}
	source, ok := record["source"].(map[string]any)
	if !ok {
		t.Fatalf("source is missing: %#v", record)
	}
	if source["function"] != "requestLoggingSourceHandler" {
		t.Errorf("source function = %v, want requestLoggingSourceHandler", source["function"])
	}
	if source["file"] != "internal/middleware/request_logging_test.go" {
		t.Errorf("source file = %v, want internal/middleware/request_logging_test.go", source["file"])
	}
}

func requestLoggingSourceHandler(w http.ResponseWriter, r *http.Request) {
	SetRequestSource(r.Context(), reflect.ValueOf(requestLoggingSourceHandler).Pointer())
	w.WriteHeader(http.StatusAccepted)
}
