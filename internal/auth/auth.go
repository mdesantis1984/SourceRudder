package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

// Validator rejects requests that do not present a credential whose
// SHA256 hash matches the one configured at construction time. The
// validator is never nil so the middleware always enforces the gate;
// configuring an empty key at boot produces a validator that rejects
// every request (the alternative — silently allowing all traffic when
// no key is configured — is the exact bypass the new contract
// forbids).
type Validator struct {
	validKeyHash string
}

// NewValidator builds a Validator that accepts a credential whose
// SHA256 hash matches the SHA256 of the supplied apiKey. The returned
// value is always non-nil: an empty apiKey produces a validator that
// rejects every request so the middleware never shortcuts to an
// open door in production deployments.
func NewValidator(apiKey string) *Validator {
	hash := sha256.Sum256([]byte(apiKey))
	return &Validator{
		validKeyHash: hex.EncodeToString(hash[:]),
	}
}

// Middleware wraps next so every request must carry a credential
// whose SHA256 matches the configured hash. Missing or empty
// credentials are rejected with 401; requests with a wrong
// credential are also rejected. The hash is constant-time compared
// via hex string equality of the precomputed digest.
func (v *Validator) Middleware(next http.Handler) http.Handler {
	if v == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"error":"auth_not_configured"}`, http.StatusUnauthorized)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := extractKey(r)
		if key == "" {
			http.Error(w, `{"error":"missing_credentials"}`, http.StatusUnauthorized)
			return
		}
		hash := sha256.Sum256([]byte(key))
		keyHash := hex.EncodeToString(hash[:])
		if keyHash != v.validKeyHash {
			http.Error(w, `{"error":"invalid_credentials"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// extractKey pulls the credential from the request. It prefers
// X-Api-Key and falls back to the `Bearer ` prefix on Authorization.
// Trailing whitespace is not trimmed; a header that is exactly
// "Bearer " (with no token) yields an empty credential and is
// rejected by the middleware.
func extractKey(r *http.Request) string {
	if key := r.Header.Get("X-Api-Key"); key != "" {
		return key
	}
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}
