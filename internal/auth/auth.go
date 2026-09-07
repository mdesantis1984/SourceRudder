package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

// Validator rejects requests that do not present a credential whose
// value matches the one configured at construction time. The
// validator is never nil so the middleware always enforces the gate;
// configuring an empty key at boot produces a validator that rejects
// every request (the alternative — silently allowing all traffic when
// no key is configured — is the exact bypass the new contract
// forbids).
type Validator struct {
	verificationKey [32]byte
	validKeyMAC     [32]byte
}

// NewValidator builds a Validator that accepts a credential whose
// value matches the supplied apiKey. The returned value is always
// non-nil: an empty apiKey produces a validator that
// rejects every request so the middleware never shortcuts to an
// open door in production deployments.
func NewValidator(apiKey string) *Validator {
	v := &Validator{}
	if _, err := rand.Read(v.verificationKey[:]); err != nil {
		panic("auth: cannot initialize credential verifier")
	}
	v.validKeyMAC = v.keyMAC(apiKey)
	return v
}

func (v *Validator) keyMAC(key string) [32]byte {
	mac := hmac.New(sha256.New, v.verificationKey[:])
	_, _ = mac.Write([]byte(key))
	var sum [32]byte
	copy(sum[:], mac.Sum(nil))
	return sum
}

// Middleware wraps next so every request must carry a credential
// matching the configured key. Missing, empty, or wrong credentials
// are rejected with 401. Matching uses a constant-time byte comparison.
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
		keyMAC := v.keyMAC(key)
		if subtle.ConstantTimeCompare(keyMAC[:], v.validKeyMAC[:]) != 1 {
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
