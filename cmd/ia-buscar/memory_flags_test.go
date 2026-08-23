package main

import "testing"

// TestMainMemoryFlagsReadEnvDefaults covers the env-default path of
// the -memory-url / -memory-apikey flag wiring. When the operator
// does not pass explicit flags, the values MUST come from
// MEMORY_URL / MEMORY_APIKEY env vars. The helper is split out so the
// precedence rule is exercised without re-declaring flags.
func TestMainMemoryFlagsReadEnvDefaults(t *testing.T) {
	t.Setenv("MEMORY_URL", "http://memory.local:7438")
	t.Setenv("MEMORY_APIKEY", "env-key-42")

	url, key := resolveMemoryConfig("", "")
	if url != "http://memory.local:7438" {
		t.Fatalf("URL from env: got %q, want http://memory.local:7438", url)
	}
	if key != "env-key-42" {
		t.Fatalf("APIKey from env: got %q, want env-key-42", key)
	}
}

// TestMainMemoryFlagsOverrideEnv locks in the precedence rule: when
// the operator passes explicit flags, those values MUST win over the
// env vars. This is the same shape as the FETCH_TIMEOUT_MS override
// path; it guarantees an operator's CLI override is never silently
// dropped.
func TestMainMemoryFlagsOverrideEnv(t *testing.T) {
	t.Setenv("MEMORY_URL", "http://memory.local:7438")
	t.Setenv("MEMORY_APIKEY", "env-key-42")

	url, key := resolveMemoryConfig("http://flag-host:9999", "flag-key-99")
	if url != "http://flag-host:9999" {
		t.Fatalf("flag URL must win: got %q, want http://flag-host:9999", url)
	}
	if key != "flag-key-99" {
		t.Fatalf("flag apikey must win: got %q, want flag-key-99", key)
	}
}

// TestMainMemoryFlagsBothEmpty covers the "neither flag nor env set"
// path: the helper returns empty strings so the caller can construct
// a no-op memory client. Triangulates against the env path so a
// regression that always reads env (or never reads env) surfaces
// here.
func TestMainMemoryFlagsBothEmpty(t *testing.T) {
	t.Setenv("MEMORY_URL", "")
	t.Setenv("MEMORY_APIKEY", "")

	url, key := resolveMemoryConfig("", "")
	if url != "" {
		t.Fatalf("expected empty URL when both flag and env are empty, got %q", url)
	}
	if key != "" {
		t.Fatalf("expected empty apikey when both flag and env are empty, got %q", key)
	}
}
