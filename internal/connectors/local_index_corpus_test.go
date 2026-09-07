package connectors

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalIndexRejectsUnsafeCorpora(t *testing.T) {
	tests := map[string]string{
		"unknown version": `{"version":2,"documents":[]}`,
		"unknown field":   `{"version":1,"documents":[],"secret":"value"}`,
		"duplicate id":    `{"version":1,"documents":[{"id":"same","title":"A"},{"id":"same","title":"B"}]}`,
		"unsafe id":       `{"version":1,"documents":[{"id":"../../file","title":"A"}]}`,
		"file URL":        `{"version":1,"documents":[{"id":"a","title":"A","url":"file:///private/path"}]}`,
		"URL credentials": `{"version":1,"documents":[{"id":"a","title":"A","url":"https://user:pass@example.com/doc"}]}`,
		"URL query":       `{"version":1,"documents":[{"id":"a","title":"A","url":"https://example.com/doc?token=value"}]}`,
		"trailing JSON":   `{"version":1,"documents":[]} {}`,
	}
	for name, corpus := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := NewLocalIndexConnector(writeLocalIndexCorpus(t, corpus)); err == nil {
				t.Fatal("expected corpus rejection")
			}
		})
	}
}

func TestLocalIndexRejectsSymlinkWritableAndOversizedFiles(t *testing.T) {
	target := writeLocalIndexCorpus(t, `{"version":1,"documents":[]}`)
	symlink := filepath.Join(t.TempDir(), "index.json")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if _, err := NewLocalIndexConnector(symlink); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}

	writable := writeLocalIndexCorpus(t, `{"version":1,"documents":[]}`)
	if err := os.Chmod(writable, 0o666); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	if _, err := NewLocalIndexConnector(writable); err == nil || !strings.Contains(err.Error(), "writable") {
		t.Fatalf("expected writable-file rejection, got %v", err)
	}

	oversized := filepath.Join(t.TempDir(), "oversized.json")
	if err := os.WriteFile(oversized, []byte(strings.Repeat("x", maxLocalIndexBytes+1)), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := NewLocalIndexConnector(oversized); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected size rejection, got %v", err)
	}
}

func writeLocalIndexCorpus(t *testing.T, corpus string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.json")
	if err := os.WriteFile(path, []byte(corpus), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}
