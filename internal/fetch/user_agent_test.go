package fetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func receivedUserAgent(t *testing.T, cfg Config) string {
	t.Helper()
	withSSRFDisabled(t)
	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case got <- r.Header.Get("User-Agent"):
		default:
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><head><title>UA</title></head><body>x</body></html>"))
	}))
	defer srv.Close()

	f := NewFetcherServiceWithConfig(cfg)
	if _, err := f.Fetch(context.Background(), srv.URL); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	select {
	case ua := <-got:
		return ua
	default:
		t.Fatal("upstream never recorded the User-Agent header")
		return ""
	}
}

// S5: with no configured User-Agent the upstream sees a desktop browser.
func TestFetchDefaultUserAgentIsDesktopBrowser(t *testing.T) {
	for name, cfg := range map[string]Config{
		"config-without-ua": {TimeoutMs: 5000},
	} {
		ua := receivedUserAgent(t, cfg)
		if !strings.HasPrefix(ua, "Mozilla/5.0 (") {
			t.Errorf("%s: default User-Agent must be a browser string starting with Mozilla/5.0, got %q", name, ua)
		}
		if !strings.Contains(ua, "Chrome/") || !strings.Contains(ua, "Safari/") {
			t.Errorf("%s: default User-Agent must look like desktop Chrome, got %q", name, ua)
		}
		if strings.Contains(ua, "Mobile") || strings.Contains(ua, "SourceRudder") {
			t.Errorf("%s: default User-Agent must be desktop and not bot-branded, got %q", name, ua)
		}
	}
}

// S5: the legacy constructor gets the same browser default.
func TestFetchLegacyConstructorDefaultUserAgentIsBrowser(t *testing.T) {
	withSSRFDisabled(t)
	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case got <- r.Header.Get("User-Agent"):
		default:
		}
		_, _ = w.Write([]byte("<html><body>x</body></html>"))
	}))
	defer srv.Close()
	if _, err := NewFetcherService(5000).Fetch(context.Background(), srv.URL); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if ua := <-got; !strings.HasPrefix(ua, "Mozilla/5.0 (") {
		t.Fatalf("legacy default User-Agent must be a browser string, got %q", ua)
	}
}

// S5: an explicit User-Agent (FETCH_USER_AGENT) is honored unchanged.
func TestFetchExplicitUserAgentHonoredUnchanged(t *testing.T) {
	const explicit = "SourceRudder/3.0.0"
	if ua := receivedUserAgent(t, Config{UserAgent: explicit, TimeoutMs: 5000}); ua != explicit {
		t.Fatalf("explicit User-Agent must be sent unchanged: want %q, got %q", explicit, ua)
	}
}
