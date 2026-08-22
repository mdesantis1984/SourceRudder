package types

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestFetchResponseWireFieldsSerializeAndUnmarshal is the wire
// contract RED gate. Every documented field on FetchResponse MUST
// survive a JSON round-trip so MCP clients (and the fetcher's
// caller) can rely on the field semantics. The previous shape only
// constructed the struct and asserted addressability, which proves
// nothing about the wire contract — a future tag change could
// silently rename a field and the old test would still pass.
func TestFetchResponseWireFieldsSerializeAndUnmarshal(t *testing.T) {
	original := FetchResponse{
		URL:      "https://example.com/page",
		Outcome:  "success",
		Status:   200,
		Attempts: 1,
	}
	if original.URL == "" {
		t.Fatal("URL is empty")
	}

	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// JSON keys are camelCase per the field tags; assert the
	// documented names so an accidental json tag rename breaks the
	// test loudly.
	for _, want := range []string{`"url":`, `"outcome":`, `"status":`, `"attempts":`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("wire JSON missing %q: %s", want, raw)
		}
	}

	var decoded FetchResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.URL != original.URL {
		t.Fatalf("URL round-trip: got %q, want %q", decoded.URL, original.URL)
	}
	if decoded.Outcome != original.Outcome {
		t.Fatalf("Outcome round-trip: got %q, want %q", decoded.Outcome, original.Outcome)
	}
	if decoded.Status != original.Status {
		t.Fatalf("Status round-trip: got %d, want %d", decoded.Status, original.Status)
	}
	if decoded.Attempts != original.Attempts {
		t.Fatalf("Attempts round-trip: got %d, want %d", decoded.Attempts, original.Attempts)
	}
}

// TestFetchResponseOmitsEmptyOptionalFieldsKeepsBackCompat locks in
// the omitempty contract for optional fields (Title, Content,
// Metadata, Warnings, RedirectChain) so a future regression that
// drops omitempty cannot accidentally bloat the wire payload or
// surface nil slices on the client.
func TestFetchResponseOmitsEmptyOptionalFieldsKeepsBackCompat(t *testing.T) {
	original := FetchResponse{
		URL:     "https://example.com",
		Outcome: "success",
		Status:  200,
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Optional fields MUST be absent from the wire when zero.
	for _, banned := range []string{`"title":`, `"content":`, `"metadata":`, `"warnings":`, `"redirectChain":`} {
		if strings.Contains(string(raw), banned) {
			t.Fatalf("empty optional field %q leaked into wire JSON: %s", banned, raw)
		}
	}
}