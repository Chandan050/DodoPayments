package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"dodo-payments/internal/logging"
)

type Result struct {
	Status       string `json:"status"`
	PSPReference string `json:"psp_ref"`
	Code         string `json:"code"`
}

type PSP interface {
	Charge(ctx context.Context, token string, amountCents int64, currency, idempotencyKey string) (Result, error)
}

type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: timeout},
	}
}

func (c *Client) Charge(ctx context.Context, token string, amountCents int64, currency, idempotencyKey string) (Result, error) {
	body, err := json.Marshal(map[string]any{"token": token, "amount_cents": amountCents, "currency": currency})
	if err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/payments/charge", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", idempotencyKey)
	if traceID := logging.TraceID(ctx); traceID != "" {
		req.Header.Set("X-Trace-ID", traceID)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return Result{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{}, fmt.Errorf("PSP returned HTTP %d", resp.StatusCode)
	}
	var result Result
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return Result{}, fmt.Errorf("decode PSP response: %w", err)
	}
	if result.Status != "succeeded" && result.Status != "failed" {
		return Result{}, fmt.Errorf("PSP returned unsupported payment status %q", result.Status)
	}
	if result.Status == "failed" && result.Code == "" {
		result.Code = "unknown_error"
	}
	return result, nil
}
