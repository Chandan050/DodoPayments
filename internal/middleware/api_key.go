package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"dodo-payments/internal/model"
)

type businessContextKey struct{}

type authenticator interface {
	Authenticate(ctx context.Context, keyHash string) (*model.Business, error)
}

type APIKey struct {
	authenticator authenticator
}

func NewAPIKey(auth authenticator) *APIKey {
	return &APIKey{authenticator: auth}
}

func (m *APIKey) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimSpace(r.Header.Get("X-API-Key"))
		if key == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing X-API-Key header")
			return
		}
		business, err := m.authenticator.Authenticate(r.Context(), hashKey(key))
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid API key")
			return
		}
		ctx := context.WithValue(r.Context(), businessContextKey{}, business)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func BusinessFromContext(ctx context.Context) (*model.Business, bool) {
	business, ok := ctx.Value(businessContextKey{}).(*model.Business)
	return business, ok
}

func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
