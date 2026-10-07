# AI Usage

I used GitHub Copilot in VS Code to scaffold the Go service, propose the PostgreSQL schema, and generate boilerplate HTTP handlers and the starter migration files. I also used it to reason through the webhook signing and idempotency design and to validate the request/response payloads.

Three decisions I made myself, independent of AI suggestions:

1. I chose row-level locking on the invoice row instead of advisory locks. The AI suggested a broader advisory-lock pattern, but I decided the invoice row lock was simpler, safer, and more aligned with Postgres semantics for a single invoice payment race.
2. I kept the invoice state machine intentionally small: `open -> paid`, `open -> void`, and `open -> uncollectible`. The AI suggested a larger lifecycle with more transitional states, but I kept it tighter to match the assignment’s requirements and reduce accidental invalid transitions.
3. I used a signed webhook with HMAC-SHA256 and asynchronous delivery. The AI initially suggested a synchronous delivery path, but I rejected that because it would block the API response and violate the assignment.

One thing AI suggested that I had to correct:

The first pass suggested storing all webhook delivery attempts in memory. I corrected that by moving the queue to PostgreSQL-backed events so the system can survive restarts and retries without losing delivery state. I verified that behavior by inspecting the database schema and validating the queued-delivery flow in docker-compose.
