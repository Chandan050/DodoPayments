# Dodo Payments Backend

This project implements a minimal invoice and payment service in Go with PostgreSQL storage and a mock PSP. It is built to match the backend take-home assignment from the PDF.

## Why Go instead of Rust

I chose Go because I have four years of professional Go experience and have worked on payment systems, including migrating services from PHP to Go. That background helps me quickly understand where changes belong, trace issues through the codebase, and implement and verify payment workflows within the time available for this project.

Rust is a strong choice for systems programming, but using it here would have added language ramp-up time and made it harder for me to confidently understand and deliver the complete solution in the same short period. Go lets me focus on the payment domain concerns—such as idempotency, concurrency, and failure recovery—rather than learning a new language while building them.

Go's lightweight goroutines and straightforward concurrency model are also a good fit for concurrent request handling and background work such as payment reconciliation and webhook delivery. They provide a practical foundation for scaling these workloads as demand grows, while keeping the implementation approachable to maintain.

## Project structure

```text
cmd/
  api/                 Invoice API composition root
internal/
  config/              Environment-based configuration
  controller/          HTTP routes, request validation, and responses
  apperr/              Typed application errors and HTTP status mapping
  logging/             Structured JSON logger and request correlation
  middleware/          API-key authentication
  model/               Domain types and invoice transition rules
  payment/             Mock PSP HTTP client
  repository/          PostgreSQL queries and transactions
  service/             Billing workflows and webhook worker
  storage/             PostgreSQL connection, migrations, and demo seed
mock-psp/              Standalone mock payment processor
migrations/            PostgreSQL schema migrations
api/                   OpenAPI description
```

## Run locally

```bash
docker compose up --build
```

This starts:
- the Go invoice service on `http://localhost:8080`
- PostgreSQL on `localhost:5432`
- the mock PSP on `http://localhost:8081`

The service seeds a default business with the demo API key below:

```text
dev_business_key_0123456789
```

Send it as the `X-API-Key` header on every request.

## API examples

Create a customer:

```bash
curl -X POST http://localhost:8080/customers \
  -H 'Content-Type: application/json' \
  -H 'X-API-Key: dev_business_key_0123456789' \
  -d '{"name":"Acme Corp","email":"billing@acme.example"}'
```

Create an invoice:

```bash
curl -X POST http://localhost:8080/invoices \
  -H 'Content-Type: application/json' \
  -H 'X-API-Key: dev_business_key_0123456789' \
  -d '{
    "customer_id": "<customer-id>",
    "currency": "USD",
    "due_date": "2026-10-30",
    "line_items": [
      {"description": "Design retainer", "quantity": 2, "unit_amount_cents": 2500}
    ]
  }'
```

Attempt a successful payment:

```bash
curl -X POST http://localhost:8080/invoices/<invoice-id>/pay \
  -H 'Content-Type: application/json' \
  -H 'X-API-Key: dev_business_key_0123456789' \
  -H 'Idempotency-Key: payment-success-001' \
  -d '{"card_token":"tok_success"}'
```

Attempt a failed payment:

```bash
curl -X POST http://localhost:8080/invoices/<invoice-id>/pay \
  -H 'Content-Type: application/json' \
  -H 'X-API-Key: dev_business_key_0123456789' \
  -H 'Idempotency-Key: payment-fail-001' \
  -d '{"card_token":"tok_card_declined"}'
```

## Demo Video

Add a public Loom or equivalent shareable link here before final submission.

## API docs

See [`api/openapi.yaml`](./api/openapi.yaml) for request and response shapes.
Invoices support USD, GBP, INR, and EUR. Amounts are integer minor units; the currency is explicit on each invoice and payment attempt. If omitted when creating an invoice, currency defaults to USD for compatibility.

After a payment returns `202 Accepted`, poll `GET /invoices/<invoice-id>/payment` for the latest attempt status and retry schedule. A background worker retries ambiguous PSP outcomes with the same PSP idempotency key using increasing delays, so it can safely reconcile a charge after a timeout or service restart without a tight retry loop.

## Tests

Run unit tests with:

```bash
go test ./...
```

The PostgreSQL-backed concurrent-payment test is in [`tests/payment_concurrency_test.go`](./tests/payment_concurrency_test.go). It is skipped unless `TEST_DATABASE_URL` points to a disposable PostgreSQL database; when enabled, it migrates the schema, sends concurrent payment requests for one invoice, and checks there is only one PSP charge and payment attempt.

## Request logs and errors

API logs are JSON records with an RFC3339 timestamp, severity, message, source file and line, and a `trace_id`. A valid `X-Trace-ID` is propagated; if one is not supplied, the API generates it and returns it in the response header. Requests carrying `Idempotency-Key` include an `idempotency_key` field in the request log. Application errors use `internal/apperr` typed codes and statuses, which the controller maps to the standard `{"error":{"code":"...","message":"..."}}` response.

## Notes

- Money is stored as integer minor units (cents, pence, or paise, depending on currency). No floats are used in the payment path.
- The invoice payment route enforces idempotency with `Idempotency-Key`.
- Webhooks are signed and processed asynchronously without blocking the API response.
- Pending payment attempts and webhook deliveries are reclaimed after a worker crash.
- The mock PSP supports `tok_success`, `tok_insufficient_funds`, `tok_card_declined`, `tok_timeout`, and `tok_network_error`.
