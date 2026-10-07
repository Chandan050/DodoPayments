package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func TestJSONLogIncludesTimeTraceKeyAndCallsite(t *testing.T) {
	var output bytes.Buffer
	logger := NewJSON(&output)
	ctx := WithTraceID(context.Background(), "trace-test-1")
	ctx = WithIdempotencyKey(ctx, "payment-test-1")

	logger.Error(ctx, "payment failed", context.DeadlineExceeded)

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("log is not valid JSON: %v", err)
	}
	for _, field := range []string{"timestamp", "level", "msg", "trace_id", "idempotency_key", "source", "error"} {
		if _, ok := record[field]; !ok {
			t.Errorf("log field %q missing from %s", field, output.String())
		}
	}
	if record["trace_id"] != "trace-test-1" || record["idempotency_key"] != "payment-test-1" {
		t.Fatalf("context fields missing from log: %s", output.String())
	}
	source, ok := record["source"].(map[string]any)
	if !ok || source["file"] != "internal/logging/logger_test.go" || source["function"] != "TestJSONLogIncludesTimeTraceKeyAndCallsite" || source["line"].(float64) <= 0 {
		t.Fatalf("log source does not identify callsite: %#v", record["source"])
	}
}
