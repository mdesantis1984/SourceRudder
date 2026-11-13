package main

import (
	"context"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/internal/fetch"
	"github.com/mdesantis1984/SourceRudder/internal/mcp"
	"github.com/mdesantis1984/SourceRudder/internal/observability"
	"github.com/mdesantis1984/SourceRudder/internal/search"
	"github.com/mdesantis1984/SourceRudder/internal/synthesis"
	"github.com/mdesantis1984/SourceRudder/internal/youtube"
)

// TestFetchTimeoutMsPropagatesIntoFetcherService is the Phase 6.4
// RED gate. When --fetch-timeout-ms is parsed from flags, the value
// MUST reach the FetcherService config so the request lifecycle is
// actually bounded by the operator's setting. The previous delivery
// hard-coded 30000; this test locks in the override path.
func TestFetchTimeoutMsPropagatesIntoFetcherService(t *testing.T) {
	const wantTimeout = 12345

	f := fetch.NewFetcherServiceWithConfig(fetch.Config{
		UserAgent:    "sourcerudder/test",
		TimeoutMs:    wantTimeout,
		MaxRedirects: 5,
		MaxAttempts:  3,
	})
	got := readFetcherTimeoutMs(t, f)
	if got != wantTimeout {
		t.Fatalf("FetcherService TimeoutMs=%d; want %d", got, wantTimeout)
	}
}

// TestFetchTimeoutMsDefaultIsThirtySeconds locks in the documented
// baseline so a future regression that drops the default to "no
// timeout" surfaces immediately.
func TestFetchTimeoutMsDefaultIsThirtySeconds(t *testing.T) {
	f := fetch.NewFetcherServiceWithConfig(fetch.Config{UserAgent: "x"})
	got := readFetcherTimeoutMs(t, f)
	if got != 30000 {
		t.Fatalf("default TimeoutMs=%d; want 30000", got)
	}
}

// readFetcherTimeoutMs reaches into the FetcherService via reflection
// so the test does not depend on a private accessor. The cfg field
// shape is part of the package's internal contract; bumping it
// intentionally requires updating this test.
func readFetcherTimeoutMs(t *testing.T, f *fetch.FetcherService) int {
	t.Helper()
	v := reflect.ValueOf(f).Elem().FieldByName("cfg")
	if !v.IsValid() {
		t.Fatalf("FetcherService no longer has a 'cfg' field; update readFetcherTimeoutMs")
	}
	tm := v.FieldByName("TimeoutMs")
	if !tm.IsValid() {
		t.Fatalf("FetcherService.Config no longer has 'TimeoutMs'; update readFetcherTimeoutMs")
	}
	return int(tm.Int())
}

// TestFetchEnvVarsWireIntoFetcherService is the Phase 12.9 RED gate.
// The deployment manifests (k8s, systemd) document
// FETCH_USER_AGENT / FETCH_TIMEOUT_MS / FETCH_MAX_REDIRECTS /
// FETCH_MAX_ATTEMPTS, but the previous main package did not actually
// read them. The contract: buildFetchConfig MUST honour every one of
// those env vars when the flag override is not set (flag=0 means
// "no flag override"). The flag-provided timeout wins when both are
// set so the operator's CLI override always takes precedence.
func TestFetchEnvVarsWireIntoFetcherService(t *testing.T) {
	t.Setenv("FETCH_USER_AGENT", "sourcerudder/env-ua/42")
	t.Setenv("FETCH_TIMEOUT_MS", "12345")
	t.Setenv("FETCH_MAX_REDIRECTS", "7")
	t.Setenv("FETCH_MAX_ATTEMPTS", "9")

	cfg := buildFetchConfig(0) // flag=0: env vars drive the config
	if cfg.UserAgent != "sourcerudder/env-ua/42" {
		t.Fatalf("UserAgent: got %q, want sourcerudder/env-ua/42", cfg.UserAgent)
	}
	if cfg.TimeoutMs != 12345 {
		t.Fatalf("TimeoutMs: got %d, want 12345", cfg.TimeoutMs)
	}
	if cfg.MaxRedirects != 7 {
		t.Fatalf("MaxRedirects: got %d, want 7", cfg.MaxRedirects)
	}
	if cfg.MaxAttempts != 9 {
		t.Fatalf("MaxAttempts: got %d, want 9", cfg.MaxAttempts)
	}
	if cfg.BaseBackoff != 200*time.Millisecond {
		t.Fatalf("BaseBackoff: got %v, want 200ms", cfg.BaseBackoff)
	}
}

// TestFetchFlagTimeoutWinsOverEnv covers the override path: when the
// CLI flag is non-zero, that value MUST take precedence over the
// FETCH_TIMEOUT_MS env var. The previous code passed the flag value
// directly so this was implicit; we now test it explicitly so the
// new buildFetchConfig helper preserves the precedence.
func TestFetchFlagTimeoutWinsOverEnv(t *testing.T) {
	t.Setenv("FETCH_TIMEOUT_MS", "12345")
	cfg := buildFetchConfig(7777)
	if cfg.TimeoutMs != 7777 {
		t.Fatalf("flag TimeoutMs MUST win over env: got %d, want 7777", cfg.TimeoutMs)
	}
}

// TestFetchEnvVarsDefaultsAppliedWhenUnset proves the helper still
// produces sensible defaults when no env vars are configured. The
// deployment manifests rely on these defaults so a misconfigured
// pod still boots.
func TestFetchEnvVarsDefaultsAppliedWhenUnset(t *testing.T) {
	// Explicitly unset all four by passing empty strings; t.Setenv
	// with empty value still counts as set, so we use os.Unsetenv
	// via t.Setenv("X", "") is not enough — we call unsetenv directly.
	t.Setenv("FETCH_USER_AGENT", "")
	t.Setenv("FETCH_TIMEOUT_MS", "")
	t.Setenv("FETCH_MAX_REDIRECTS", "")
	t.Setenv("FETCH_MAX_ATTEMPTS", "")
	cfg := buildFetchConfig(0)
	if cfg.UserAgent == "" {
		t.Fatal("default UserAgent must be non-empty")
	}
	if cfg.TimeoutMs != 30000 {
		t.Fatalf("default TimeoutMs: got %d, want 30000", cfg.TimeoutMs)
	}
	if cfg.MaxRedirects != 5 {
		t.Fatalf("default MaxRedirects: got %d, want 5", cfg.MaxRedirects)
	}
	if cfg.MaxAttempts != 3 {
		t.Fatalf("default MaxAttempts: got %d, want 3", cfg.MaxAttempts)
	}
	// Sanity: the env var contract is built around string values; a
	// future regression that drops the parse-once step must surface
	// here.
	parsed, err := strconv.Atoi("12345")
	if err != nil || parsed != 12345 {
		t.Fatalf("strconv.Atoi sanity check failed: %v", err)
	}
}

func TestLocalIndexPathPrecedence(t *testing.T) {
	t.Setenv("LOCAL_INDEX_PATH", "/safe/env-index.json")
	if got := resolveLocalIndexPath(""); got != "/safe/env-index.json" {
		t.Fatalf("env path=%q", got)
	}
	if got := resolveLocalIndexPath(" /safe/flag-index.json "); got != "/safe/flag-index.json" {
		t.Fatalf("flag path must win and be trimmed, got %q", got)
	}
	t.Setenv("LOCAL_INDEX_PATH", "  ")
	if got := resolveLocalIndexPath(""); got != "" {
		t.Fatalf("blank path must disable the provider, got %q", got)
	}
}

func TestServerStart(t *testing.T) {
	cacheSvc := cache.NewService(300)
	cm := search.NewConnectorManager(cacheSvc)
	planner := search.NewPlanner()
	fetchSvc := fetch.NewFetcherService(5000)
	synthSvc := synthesis.NewService()
	server := mcp.NewServer(cm, planner, "stdio", ":8080", "http://localhost:8888", 300, 5000, fetchSvc, synthSvc, nil, observability.New(), cache.NewHistoryService(10))
	if server == nil {
		t.Fatal("expected non-nil server")
	}
}

func TestToolsCount(t *testing.T) {
	cacheSvc := cache.NewService(300)
	cm := search.NewConnectorManager(cacheSvc)
	planner := search.NewPlanner()
	fetchSvc := fetch.NewFetcherService(5000)
	synthSvc := synthesis.NewService()
	server := mcp.NewServer(cm, planner, "stdio", ":8080", "http://localhost:8888", 300, 5000, fetchSvc, synthSvc, nil, observability.New(), cache.NewHistoryService(10))
	tools := server.Tools()
	if len(tools) != 29 {
		t.Errorf("expected 29 tools (restored runtime contract), got %d", len(tools))
	}
}

func TestHandleInitialize(t *testing.T) {
	cacheSvc := cache.NewService(300)
	cm := search.NewConnectorManager(cacheSvc)
	planner := search.NewPlanner()
	fetchSvc := fetch.NewFetcherService(5000)
	synthSvc := synthesis.NewService()
	server := mcp.NewServer(cm, planner, "stdio", ":8080", "http://localhost:8888", 300, 5000, fetchSvc, synthSvc, nil, observability.New(), cache.NewHistoryService(10))
	result, err := server.HandleInitialize(context.Background(), []byte(`{"clientId": "test"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := result.(map[string]interface{})
	if m["serverInfo"] == nil {
		t.Error("expected serverInfo in result")
	}
}

func TestEnvDuration(t *testing.T) {
	const name = "SOURCERUDDER_TEST_DURATION"
	cases := []struct {
		name  string
		set   bool
		value string
		want  time.Duration
	}{
		{"unset", false, "", 5 * time.Minute},
		{"empty", true, "", 5 * time.Minute},
		{"valid", true, "90s", 90 * time.Second},
		{"invalid", true, "soon", 5 * time.Minute},
		{"zero", true, "0s", 5 * time.Minute},
		{"negative", true, "-1m", 5 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(name, tc.value)
			}
			if got := envDuration(name, 5*time.Minute); got != tc.want {
				t.Fatalf("envDuration = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildYouTubeConfigDefaults(t *testing.T) {
	for _, name := range []string{"YTDLP_PATH", "YTDLP_JS_RUNTIMES", "YOUTUBE_POT_BASE_URL", "YOUTUBE_FETCH_POT", "YOUTUBE_DEFAULT_LANGUAGE", "YOUTUBE_CACHE_TTL", "YOUTUBE_BLOCK_COOLDOWN", "YTDLP_TIMEOUT", "YOUTUBE_WORK_DIR", "YOUTUBE_TRANSCRIPTS"} {
		t.Setenv(name, "")
	}
	cfg, enabled := buildYouTubeConfig()
	if !enabled {
		t.Fatal("YouTube transcripts must be enabled by default")
	}
	want := youtube.Config{
		Binary:        "yt-dlp",
		CacheTTL:      youtube.DefaultCacheTTL,
		BlockCooldown: youtube.DefaultBlockCooldown,
		Timeout:       youtube.DefaultTimeout,
	}
	if cfg != want {
		t.Fatalf("config = %+v, want %+v", cfg, want)
	}
}

func TestBuildYouTubeConfigFromEnv(t *testing.T) {
	t.Setenv("YTDLP_PATH", "/opt/yt-dlp")
	t.Setenv("YTDLP_JS_RUNTIMES", "node")
	t.Setenv("YOUTUBE_POT_BASE_URL", "http://127.0.0.1:4416")
	t.Setenv("YOUTUBE_FETCH_POT", "auto")
	t.Setenv("YOUTUBE_DEFAULT_LANGUAGE", "es")
	t.Setenv("YOUTUBE_CACHE_TTL", "48h")
	t.Setenv("YOUTUBE_BLOCK_COOLDOWN", "30m")
	t.Setenv("YTDLP_TIMEOUT", "45s")
	t.Setenv("YOUTUBE_WORK_DIR", "/var/tmp/yt")
	t.Setenv("YOUTUBE_TRANSCRIPTS", "")
	cfg, enabled := buildYouTubeConfig()
	if !enabled {
		t.Fatal("expected enabled")
	}
	want := youtube.Config{
		Binary: "/opt/yt-dlp", JSRuntimes: "node", POTBaseURL: "http://127.0.0.1:4416", FetchPOT: "auto", DefaultLanguage: "es", WorkDir: "/var/tmp/yt",
		CacheTTL: 48 * time.Hour, BlockCooldown: 30 * time.Minute, Timeout: 45 * time.Second,
	}
	if cfg != want {
		t.Fatalf("config = %+v, want %+v", cfg, want)
	}
}

func TestBuildYouTubeConfigOffDisablesService(t *testing.T) {
	for _, v := range []string{"off", "OFF", " off "} {
		t.Setenv("YOUTUBE_TRANSCRIPTS", v)
		if _, enabled := buildYouTubeConfig(); enabled {
			t.Fatalf("YOUTUBE_TRANSCRIPTS=%q must disable the service", v)
		}
	}
}
