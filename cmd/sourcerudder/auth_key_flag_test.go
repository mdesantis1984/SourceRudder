package main

import (
	"bytes"
	"go/parser"
	"go/token"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/mdesantis1984/SourceRudder/internal/auth"
)

// authKeyEnv is the env var name the binary reads when -auth-key is
// empty. Declared in production main.go; redeclared here so the tests
// stay self-contained and do not need an exported symbol just to read
// a constant.
const authKeyEnv = "SOURCERUDDER_AUTH_KEY"

// TestResolveAuthKeyEnvFallback covers the RED gate for the CT201
// argv-leak fix: when the operator does NOT pass -auth-key, the API
// key MUST come from the SOURCERUDDER_AUTH_KEY env var. This is the
// primary path operators are expected to use so the secret never
// reaches argv (and therefore ps aux / process listings / shell
// history).
func TestResolveAuthKeyEnvFallback(t *testing.T) {
	t.Setenv(authKeyEnv, "env-secret-99")

	got := resolveAuthKey("")
	if got != "env-secret-99" {
		t.Fatalf("env fallback: got %q, want env-secret-99", got)
	}
}

// TestResolveAuthKeyTrimsEnvWhitespace locks in the "trim whitespace"
// contract. A trailing newline from a secrets file or a leading space
// from a copy-paste must not silently produce a key that fails every
// validator comparison.
func TestResolveAuthKeyTrimsEnvWhitespace(t *testing.T) {
	t.Setenv(authKeyEnv, "  \tenv-secret-42\n")

	got := resolveAuthKey("")
	if got != "env-secret-42" {
		t.Fatalf("trim: got %q, want env-secret-42", got)
	}
}

// TestResolveAuthKeyFlagWinsOverEnv is the precedence rule. An
// operator's explicit -auth-key CLI value MUST take precedence over
// the env var so existing scripts that pass the flag keep working.
// The flag value is NOT trimmed (exact-string backward compatibility).
func TestResolveAuthKeyFlagWinsOverEnv(t *testing.T) {
	t.Setenv(authKeyEnv, "env-secret-99")

	got := resolveAuthKey("flag-secret-7")
	if got != "flag-secret-7" {
		t.Fatalf("flag precedence: got %q, want flag-secret-7", got)
	}
}

// TestResolveAuthKeyFlagExactPreserved covers the no-trim rule for the
// flag path. A literal key with internal whitespace is unusual but
// legal; trimming would silently break authentication for any operator
// who relies on it.
func TestResolveAuthKeyFlagExactPreserved(t *testing.T) {
	t.Setenv(authKeyEnv, "env-secret-99")

	const literal = "  flag-with-spaces  "
	got := resolveAuthKey(literal)
	if got != literal {
		t.Fatalf("flag must NOT be trimmed: got %q, want %q", got, literal)
	}
}

// TestResolveAuthKeyBlankEnvFailsClosed covers the documented fail-
// closed contract: when the flag is empty AND the env var is unset OR
// only whitespace, the helper MUST return an empty string so
// auth.NewValidator (which rejects every request for an empty key)
// preserves the no-bypass guarantee. This is the regression test for
// the production rule that "no key configured" must NEVER become
// "allow all traffic".
func TestResolveAuthKeyBlankEnvFailsClosed(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		t.Setenv(authKeyEnv, "")
		if got := resolveAuthKey(""); got != "" {
			t.Fatalf("empty env must fail closed: got %q, want \"\"", got)
		}
	})
	t.Run("whitespace-only", func(t *testing.T) {
		t.Setenv(authKeyEnv, "   \t\n  ")
		if got := resolveAuthKey(""); got != "" {
			t.Fatalf("whitespace-only env must fail closed: got %q, want \"\"", got)
		}
	})
	t.Run("unset", func(t *testing.T) {
		// Genuinely unset, not the t.Setenv("","") equivalent.
		prev, prevOK := os.LookupEnv(authKeyEnv)
		if err := os.Unsetenv(authKeyEnv); err != nil {
			t.Fatalf("unsetenv: %v", err)
		}
		t.Cleanup(func() {
			if prevOK {
				_ = os.Setenv(authKeyEnv, prev)
			} else {
				_ = os.Unsetenv(authKeyEnv)
			}
		})
		// Sanity: must take the ok=false branch (a regression
		// fails here, not via silent string equality).
		if _, ok := os.LookupEnv(authKeyEnv); ok {
			t.Fatalf("precondition broken: %s must be genuinely unset", authKeyEnv)
		}
		if got := resolveAuthKey(""); got != "" {
			t.Fatalf("unset env must fail closed: got %q, want \"\"", got)
		}
	})
}

// TestResolveAuthKeyValidatorWiring proves the helper output reaches
// auth.NewValidator correctly and reaches the protected handler.
func TestResolveAuthKeyValidatorWiring(t *testing.T) {
	const envKey = "wiring-secret-abc"
	t.Setenv(authKeyEnv, envKey)

	resolved := resolveAuthKey("")
	v := auth.NewValidator(resolved)

	if v == nil {
		t.Fatal("validator must be non-nil")
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.com/mcp", nil)
	req.Header.Set("X-Api-Key", envKey)
	rec := httptest.NewRecorder()
	called := false
	v.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !called {
		t.Fatalf("resolved key did not reach protected handler: status=%d called=%v", rec.Code, called)
	}
}

// TestResolveAuthKeyNeverLogged is the documented-diagnostics guard.
// The CT201 fix MUST not regress into "we fixed argv but now the key
// ends up in stdout". This test exercises two surfaces:
//
//  1. Static: the helper file does not contain any log.Print* call
//     referencing the resolved key. We read the file at test time so
//     a regression surfaces immediately.
//  2. Dynamic: while the helper itself is a pure function (so it
//     does not log), we route the package's default logger to a
//     buffer for the duration of the test and confirm no secret
//     strings appear in the captured stream after a representative
//     call.
func TestResolveAuthKeyNeverLogged(t *testing.T) {
	const envSecret = "diagnostic-secret-XYZ"
	const flagSecret = "diagnostic-flag-ABC"
	t.Setenv(authKeyEnv, envSecret)

	// Static guard: main.go must not contain a log.Print* call that
	// references the resolved key path. We look for forbidden
	// patterns that would dump the secret to stdout/stderr.
	mainSrc := readMainSource(t)
	forbidden := []string{
		"log.Print(*authKey)",
		"log.Printf(\"%s\", *authKey)",
		"log.Println(*authKey)",
		"log.Print(resolved)",
		"log.Printf(\"%s\", resolved)",
		"log.Println(resolved)",
	}
	for _, frag := range forbidden {
		if strings.Contains(mainSrc, frag) {
			t.Fatalf("main.go contains forbidden log statement %q that would leak the auth key", frag)
		}
	}
	// Dynamic guard: redirect the package logger, call the helper,
	// and verify the secret never appears in captured output.
	buf := captureLog(t)
	_ = resolveAuthKey(flagSecret)
	_ = resolveAuthKey("") // env path also exercised
	out := buf.String()
	if strings.Contains(out, envSecret) {
		t.Fatalf("env-supplied auth key %q appeared in logger output:\n%s", envSecret, out)
	}
	if strings.Contains(out, flagSecret) {
		t.Fatalf("flag-supplied auth key %q appeared in logger output:\n%s", flagSecret, out)
	}
}

// captureLog redirects the default logger to an in-memory buffer and
// returns a function that restores the previous writer. Caller MUST
// call restore via t.Cleanup. The returned *bytes.Buffer holds the
// captured output.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	prev := log.Writer()
	prevFlags := log.Flags()
	buf := &bytes.Buffer{}
	log.SetOutput(buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prev)
		log.SetFlags(prevFlags)
	})
	return buf
}

// readMainSource loads cmd/sourcerudder/main.go relative to the working
// directory and returns its contents. The file is parsed (and parsing
// errors surfaced) so the test fails fast if the production file is
// ever removed.
func readMainSource(t *testing.T) string {
	t.Helper()
	path := os.Getenv("MAIN_GO_PATH")
	if path == "" {
		path = "main.go"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	// Parse to confirm it is still valid Go.
	if _, err := parser.ParseFile(token.NewFileSet(), path, data, parser.AllErrors); err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	return string(data)
}
