package logging

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Logger struct {
	handler slog.Handler
}

type traceIDKey struct{}
type idempotencyKey struct{}

func NewJSON(w io.Writer) *Logger {
	return &Logger{
		handler: slog.NewJSONHandler(w, &slog.HandlerOptions{
			Level: slog.LevelInfo,
			ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
				if attr.Key == slog.TimeKey {
					attr.Key = "timestamp"
				}
				return attr
			},
		}),
	}
}

func relativeSourceFile(file string) string {
	file = filepath.ToSlash(file)
	for _, directory := range []string{"/internal/", "/cmd/", "/mock-psp/"} {
		if index := strings.LastIndex(file, directory); index >= 0 {
			return file[index+1:]
		}
	}
	return strings.TrimLeft(file, "/")
}

func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey{}, traceID)
}

func TraceID(ctx context.Context) string {
	traceID, _ := ctx.Value(traceIDKey{}).(string)
	return traceID
}

func WithIdempotencyKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, idempotencyKey{}, key)
}

func (l *Logger) Info(ctx context.Context, message string, attrs ...slog.Attr) {
	l.log(ctx, slog.LevelInfo, message, nil, 0, attrs...)
}

func (l *Logger) InfoAt(ctx context.Context, pc uintptr, message string, attrs ...slog.Attr) {
	l.log(ctx, slog.LevelInfo, message, nil, pc, attrs...)
}

func (l *Logger) Error(ctx context.Context, message string, err error, attrs ...slog.Attr) {
	l.log(ctx, slog.LevelError, message, err, 0, attrs...)
}

func (l *Logger) log(ctx context.Context, level slog.Level, message string, err error, pc uintptr, attrs ...slog.Attr) {
	if l == nil || l.handler == nil || !l.handler.Enabled(ctx, level) {
		return
	}
	if traceID, ok := ctx.Value(traceIDKey{}).(string); ok && traceID != "" {
		attrs = append(attrs, slog.String("trace_id", traceID))
	} else {
		attrs = append(attrs, slog.String("trace_id", uuid.NewString()))
	}
	if key, ok := ctx.Value(idempotencyKey{}).(string); ok && key != "" {
		attrs = append(attrs, slog.String("idempotency_key", key))
	}
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
	}

	if pc == 0 {
		var pcs [1]uintptr
		runtime.Callers(3, pcs[:])
		pc = pcs[0]
	}
	frame, _ := runtime.CallersFrames([]uintptr{pc}).Next()
	function := frame.Function
	if separator := strings.LastIndex(function, "."); separator >= 0 {
		function = function[separator+1:]
	}
	record := slog.NewRecord(time.Now(), level, message, pc)
	record.AddAttrs(slog.Group("source",
		slog.String("function", function),
		slog.String("file", relativeSourceFile(frame.File)),
		slog.Int("line", frame.Line),
	))
	record.AddAttrs(attrs...)
	_ = l.handler.Handle(ctx, record)
}
