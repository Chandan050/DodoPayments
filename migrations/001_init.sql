CREATE TABLE IF NOT EXISTS businesses (
    id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    api_key_prefix VARCHAR(32) NOT NULL,
    api_key_hash VARCHAR(128) NOT NULL,
    revoked_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS customers (
    id UUID PRIMARY KEY,
    business_id UUID NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    email VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_customers_business ON customers (business_id);

CREATE TABLE IF NOT EXISTS invoices (
    id UUID PRIMARY KEY,
    business_id UUID NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
    customer_id UUID NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    currency CHAR(3) NOT NULL DEFAULT 'USD',
    due_date DATE NOT NULL,
    total_cents BIGINT NOT NULL CHECK (total_cents >= 0),
    state VARCHAR(32) NOT NULL DEFAULT 'open',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_invoices_business_state ON invoices (business_id, state);

CREATE TABLE IF NOT EXISTS invoice_line_items (
    id UUID PRIMARY KEY,
    invoice_id UUID NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    description VARCHAR(255) NOT NULL,
    quantity BIGINT NOT NULL CHECK (quantity > 0),
    unit_amount_cents BIGINT NOT NULL CHECK (unit_amount_cents >= 0)
);
CREATE INDEX IF NOT EXISTS idx_invoice_line_items_invoice ON invoice_line_items (invoice_id);

CREATE TABLE IF NOT EXISTS payment_attempts (
    id UUID PRIMARY KEY,
    invoice_id UUID NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    business_id UUID NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
    idempotency_key VARCHAR(255) NOT NULL,
    token VARCHAR(128) NOT NULL,
    status VARCHAR(32) NOT NULL,
    amount_cents BIGINT NOT NULL CHECK (amount_cents >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    retry_count INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    error_code VARCHAR(64),
    psp_reference VARCHAR(128),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_payment_attempts_invoice ON payment_attempts (invoice_id, created_at DESC);

CREATE TABLE IF NOT EXISTS webhook_endpoints (
    id UUID PRIMARY KEY,
    business_id UUID NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
    url TEXT NOT NULL,
    secret VARCHAR(128) NOT NULL,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS webhook_events (
    id UUID PRIMARY KEY,
    business_id UUID NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
    event_type VARCHAR(64) NOT NULL,
    payload_json JSONB NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    attempts INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_webhook_events_pending ON webhook_events (status, next_attempt_at);

CREATE TABLE IF NOT EXISTS idempotency_keys (
    business_id UUID NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
    invoice_id UUID NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    idempotency_key VARCHAR(255) NOT NULL,
    request_hash VARCHAR(128) NOT NULL,
    status VARCHAR(32) NOT NULL,
    response_code INT NOT NULL DEFAULT 200,
    response_body JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (business_id, invoice_id, idempotency_key)
);
