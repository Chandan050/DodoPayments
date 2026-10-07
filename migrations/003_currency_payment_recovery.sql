ALTER TABLE invoices
    ADD COLUMN IF NOT EXISTS currency CHAR(3) NOT NULL DEFAULT 'USD';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'invoices_currency_supported'
          AND conrelid = 'invoices'::regclass
    ) THEN
        ALTER TABLE invoices
            ADD CONSTRAINT invoices_currency_supported
            CHECK (currency IN ('USD', 'GBP', 'INR', 'EUR'));
    END IF;
END $$;

ALTER TABLE payment_attempts
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

ALTER TABLE payment_attempts
    ADD COLUMN IF NOT EXISTS retry_count INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

CREATE INDEX IF NOT EXISTS idx_payment_attempts_status_updated
    ON payment_attempts (status, updated_at);

CREATE INDEX IF NOT EXISTS idx_payment_attempts_recovery
    ON payment_attempts (status, next_attempt_at, updated_at);

UPDATE payment_attempts
SET status = 'processing', updated_at = NOW()
WHERE status = 'pending'
  AND EXISTS (
      SELECT 1 FROM idempotency_keys ik
      WHERE ik.business_id = payment_attempts.business_id
        AND ik.invoice_id = payment_attempts.invoice_id
        AND ik.idempotency_key = payment_attempts.idempotency_key
        AND ik.status = 'processing'
  );
