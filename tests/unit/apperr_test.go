package tests

import (
	"errors"
	"net/http"
	"testing"

	"dodo-payments/internal/apperr"
)

func TestAsPreservesTypedHTTPMappingThroughWrapping(t *testing.T) {
	err := apperr.Wrap(http.StatusConflict, "duplicate", "request conflicts", errors.New("unique constraint"))
	mapped := apperr.As(err)
	if mapped.Status != http.StatusConflict || mapped.Code != "duplicate" || mapped.Message != "request conflicts" {
		t.Fatalf("unexpected mapped error: %#v", mapped)
	}
	if !errors.Is(err, err) {
		t.Fatal("typed error should match itself")
	}
}

func TestUnknownErrorsMapToInternalError(t *testing.T) {
	mapped := apperr.As(errors.New("database details"))
	if mapped.Status != http.StatusInternalServerError || mapped.Code != "internal_error" {
		t.Fatalf("unexpected fallback mapping: %#v", mapped)
	}
	if mapped.Message == "database details" {
		t.Fatal("internal error mapping should not expose implementation details")
	}
}
