package apperr

import (
	"errors"
	"net/http"
)

type Error struct {
	Status  int
	Code    string
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

func (e *Error) Unwrap() error {
	return e.Cause
}

func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && e.Code == other.Code
}

func New(status int, code, message string, cause error) *Error {
	return &Error{Status: status, Code: code, Message: message, Cause: cause}
}

func BadRequest(code, message string) *Error {
	return New(http.StatusBadRequest, code, message, nil)
}

func Wrap(status int, code, message string, cause error) *Error {
	return New(status, code, message, cause)
}

var (
	ErrNotFound            = New(http.StatusNotFound, "not_found", "resource not found", nil)
	ErrInvalidTransition   = New(http.StatusConflict, "invalid_transition", "invoice cannot transition from its current state", nil)
	ErrIdempotencyConflict = New(http.StatusConflict, "idempotency_key_conflict", "this key was already used with a different payload", nil)
	ErrPaymentInProgress   = New(http.StatusConflict, "payment_in_progress", "a payment attempt is already pending for this invoice", nil)
)

func As(err error) *Error {
	var appError *Error
	if errors.As(err, &appError) {
		return appError
	}
	return New(http.StatusInternalServerError, "internal_error", "request could not be completed", err)
}
