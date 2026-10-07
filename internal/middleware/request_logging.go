package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"dodo-payments/internal/logging"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

type requestSourceKey struct{}

type requestSource struct {
	pc uintptr
}

func SetRequestSource(ctx context.Context, pc uintptr) {
	if source, ok := ctx.Value(requestSourceKey{}).(*requestSource); ok {
		source.pc = pc
	}
}

func (w *statusRecorder) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func RequestLogging(logger *logging.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := r.Header.Get("X-Trace-ID")
		if _, err := uuid.Parse(traceID); err != nil {
			traceID = uuid.NewString()
		}
		ctx := logging.WithTraceID(r.Context(), traceID)
		source := &requestSource{}
		ctx = context.WithValue(ctx, requestSourceKey{}, source)
		idemKey := r.Header.Get("Idempotency-Key")
		if idemKey != "" {
			ctx = logging.WithIdempotencyKey(ctx, idemKey)
		}
		w.Header().Set("X-Trace-ID", traceID)
		recorder := &statusRecorder{ResponseWriter: w}
		started := time.Now()
		next.ServeHTTP(recorder, r.WithContext(ctx))
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		logger.InfoAt(ctx, source.pc, "http request completed",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", status),
			slog.Int64("duration_ms", time.Since(started).Milliseconds()),
		)
	})
}
