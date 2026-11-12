package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/mdesantis1984/SourceRudder/internal/fetch"
	"github.com/mdesantis1984/SourceRudder/internal/observability"
	"github.com/mdesantis1984/SourceRudder/internal/youtube"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

const (
	ytTestURL = "https://youtu.be/Q-_VdeexbUI"
	ytTestVTT = "WEBVTT\n\n00:00:00.000 --> 00:00:02.000\nhola<00:00:01.000><c> mundo</c>\n\n00:00:02.000 --> 00:00:04.000\nsegunda línea\n"
)

// ytFakeRunner simulates yt-dlp by writing the files the real binary
// would write into its working directory.
type ytFakeRunner struct {
	mu       sync.Mutex
	calls    int
	failWith string // stderr returned (with an error) on every call when set
}

func (f *ytFakeRunner) Run(ctx context.Context, dir, name string, args []string) ([]byte, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.failWith != "" {
		return nil, []byte(f.failWith), errors.New("exit status 1")
	}
	var output, subLangs string
	info := false
	for i, a := range args {
		switch a {
		case "-o":
			output = args[i+1]
		case "--sub-langs":
			subLangs = args[i+1]
		case "--write-info-json":
			info = true
		}
	}
	base := strings.TrimSuffix(output, ".%(ext)s")
	if info {
		b, _ := json.Marshal(map[string]interface{}{
			"id":          "Q-_VdeexbUI",
			"title":       "Un video de prueba",
			"channel":     "Canal Ejemplo",
			"upload_date": "20250102",
			"duration":    3725.0,
			"language":    "es",
			"automatic_captions": map[string]interface{}{
				"es-orig": []interface{}{map[string]interface{}{"ext": "vtt"}},
			},
		})
		return nil, nil, os.WriteFile(filepath.Join(dir, base+".info.json"), b, 0o600)
	}
	return nil, nil, os.WriteFile(filepath.Join(dir, base+"."+subLangs+".vtt"), []byte(ytTestVTT), 0o600)
}

// newYouTubeTestServer builds a server whose fetcher is nil on purpose:
// a delegated YouTube request must never reach it.
func newYouTubeTestServer(t *testing.T, runner youtube.Runner) *Server {
	t.Helper()
	s := &Server{met: observability.New()}
	if runner != nil {
		s.SetYouTubeService(youtube.NewService(youtube.Config{Binary: "yt-dlp", WorkDir: t.TempDir()}, runner))
	}
	s.buildToolsRegistry()
	s.registerTools()
	return s
}

func callTool(t *testing.T, s *Server, name string, args map[string]interface{}) map[string]interface{} {
	t.Helper()
	return callToolCtx(t, context.Background(), s, name, args)
}

func callToolCtx(t *testing.T, ctx context.Context, s *Server, name string, args map[string]interface{}) map[string]interface{} {
	t.Helper()
	params, err := json.Marshal(map[string]interface{}{"name": name, "arguments": args})
	if err != nil {
		t.Fatal(err)
	}
	return s.handleMCPToolsCall(ctx, 1, params)
}

func resultJSON(t *testing.T, resp map[string]interface{}, into interface{}) {
	t.Helper()
	if e, ok := resp["error"]; ok {
		t.Fatalf("unexpected JSON-RPC error: %v", e)
	}
	content := resp["result"].(map[string]interface{})["content"].([]map[string]interface{})
	if err := json.Unmarshal([]byte(content[0]["text"].(string)), into); err != nil {
		t.Fatalf("decode result: %v", err)
	}
}

func errorMessage(t *testing.T, resp map[string]interface{}) string {
	t.Helper()
	e, ok := resp["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected a JSON-RPC error, got %v", resp)
	}
	return e["message"].(string)
}

func TestFetchYouTubeTranscriptToolRegisteredWithSchema(t *testing.T) {
	s := newYouTubeTestServer(t, nil)
	for _, tool := range s.Tools() {
		if tool.Name != "fetch_youtube_transcript" {
			continue
		}
		props := tool.InputSchema["properties"].(map[string]interface{})
		for _, key := range []string{"url", "language", "maxChars"} {
			if _, ok := props[key]; !ok {
				t.Errorf("schema missing property %q", key)
			}
		}
		if props["maxChars"].(map[string]interface{})["type"] != "integer" {
			t.Errorf("maxChars must be an integer")
		}
		if !reflect.DeepEqual(tool.InputSchema["required"], []string{"url"}) {
			t.Errorf("required = %v, want [url]", tool.InputSchema["required"])
		}
		lower := strings.ToLower(tool.Description)
		for _, want := range []string{"yt-dlp", "youtube_blocked", "no reintentes"} {
			if !strings.Contains(lower, want) {
				t.Errorf("description must mention %q; got %q", want, tool.Description)
			}
		}
		return
	}
	t.Fatal("fetch_youtube_transcript is not registered")
}

func TestFetchYouTubeTranscriptToolReturnsTranscript(t *testing.T) {
	r := &ytFakeRunner{}
	s := newYouTubeTestServer(t, r)

	var got youtube.Transcript
	resultJSON(t, callTool(t, s, "fetch_youtube_transcript", map[string]interface{}{"url": ytTestURL, "language": "es"}), &got)
	if got.VideoID != "Q-_VdeexbUI" || got.Title != "Un video de prueba" || got.Channel != "Canal Ejemplo" {
		t.Fatalf("unexpected metadata: %+v", got)
	}
	if got.Language != "es-orig" || got.TrackType != "automatic" || got.Cached {
		t.Fatalf("unexpected track info: %+v", got)
	}
	if !strings.Contains(got.Transcript, "hola mundo") || !strings.Contains(got.Transcript, "segunda línea") {
		t.Fatalf("transcript text missing: %q", got.Transcript)
	}
	if r.calls != 2 {
		t.Fatalf("runner calls = %d, want 2", r.calls)
	}
}

func TestFetchYouTubeTranscriptToolHonorsMaxChars(t *testing.T) {
	s := newYouTubeTestServer(t, &ytFakeRunner{})
	var got youtube.Transcript
	resultJSON(t, callTool(t, s, "fetch_youtube_transcript", map[string]interface{}{"url": ytTestURL, "maxChars": 5}), &got)
	if !got.Truncated || len(got.Transcript) == 0 || got.TotalChars <= 5 {
		t.Fatalf("expected a truncated transcript, got %+v", got)
	}
}

func TestFetchURLDelegatesYouTubeVideosAndCaches(t *testing.T) {
	for _, tool := range []string{"fetch_url", "fetch_and_extract"} {
		t.Run(tool, func(t *testing.T) {
			r := &ytFakeRunner{}
			s := newYouTubeTestServer(t, r)

			var first types.FetchResponse
			resultJSON(t, callTool(t, s, tool, map[string]interface{}{"url": ytTestURL}), &first)
			if r.calls != 2 {
				t.Fatalf("runner calls = %d, want exactly 2", r.calls)
			}
			if first.URL != "https://www.youtube.com/watch?v=Q-_VdeexbUI" || first.Title != "Un video de prueba" {
				t.Fatalf("unexpected url/title: %q / %q", first.URL, first.Title)
			}
			if first.Outcome != "success" || first.Status != 0 || first.Attempts != 0 {
				t.Fatalf("unexpected outcome/status/attempts: %+v", first)
			}
			if !strings.Contains(first.Content, "hola mundo") {
				t.Fatalf("content = %q", first.Content)
			}
			wantMeta := map[string]string{
				"source": "youtube_transcript", "videoId": "Q-_VdeexbUI", "channel": "Canal Ejemplo",
				"uploadDate": "2025-01-02", "durationSeconds": "3725", "language": "es-orig",
				"trackType": "automatic", "cached": "false", "truncated": "false",
			}
			for k, v := range wantMeta {
				if first.Metadata[k] != v {
					t.Errorf("metadata[%s] = %q, want %q", k, first.Metadata[k], v)
				}
			}
			for _, k := range []string{"duration", "totalChars"} {
				if first.Metadata[k] == "" {
					t.Errorf("metadata[%s] is empty", k)
				}
			}
			warnings := strings.Join(first.Warnings, "|")
			if !strings.Contains(warnings, "delegated to fetch_youtube_transcript: YouTube watch pages are not scraped") {
				t.Errorf("missing delegation warning: %v", first.Warnings)
			}

			var second types.FetchResponse
			resultJSON(t, callTool(t, s, tool, map[string]interface{}{"url": ytTestURL}), &second)
			if r.calls != 2 {
				t.Fatalf("cached call touched yt-dlp: calls = %d", r.calls)
			}
			if second.Metadata["cached"] != "true" {
				t.Fatalf("second call cached = %q, want true", second.Metadata["cached"])
			}
		})
	}
}

func TestFetchURLYouTubeBlockedReturnsSingleErrorWithoutFallback(t *testing.T) {
	r := &ytFakeRunner{failWith: "ERROR: unable to download webpage: HTTP Error 429: Too Many Requests"}
	s := newYouTubeTestServer(t, r) // nil fetcher: any scraping fallback would panic

	for _, tool := range []string{"fetch_url", "fetch_youtube_transcript"} {
		before := r.calls
		msg := errorMessage(t, callTool(t, s, tool, map[string]interface{}{"url": ytTestURL}))
		if !strings.Contains(msg, "youtube_blocked") {
			t.Fatalf("%s error = %q, want youtube_blocked", tool, msg)
		}
		if tool == "fetch_url" && r.calls != 1 {
			t.Fatalf("runner calls = %d, want exactly 1 (no retry)", r.calls)
		}
		if tool == "fetch_youtube_transcript" && r.calls != before {
			t.Fatalf("cooldown must stop further yt-dlp calls, calls %d -> %d", before, r.calls)
		}
	}
}

func TestFetchURLNonVideoBehavesTheSameWithAndWithoutYouTubeService(t *testing.T) {
	// Loopback is rejected by the SSRF guard, which gives a deterministic
	// fetcher outcome to compare without any network access.
	urls := []string{
		"http://127.0.0.1:1/page",
		"Q-_VdeexbUI", // bare IDs are only accepted by fetch_youtube_transcript
	}
	for _, tool := range []string{"fetch_url", "fetch_and_extract", "extract_structured"} {
		for _, u := range urls {
			plain := &Server{met: observability.New(), fetcherService: fetch.NewFetcherService(1000)}
			plain.buildToolsRegistry()
			plain.registerTools()
			r := &ytFakeRunner{}
			withYT := newYouTubeTestServer(t, r)
			withYT.fetcherService = fetch.NewFetcherService(1000)

			args := map[string]interface{}{"url": u}
			want := callTool(t, plain, tool, args)
			got := callTool(t, withYT, tool, args)
			if !reflect.DeepEqual(want, got) {
				t.Errorf("%s %q differs with the YouTube service:\nwithout: %v\nwith:    %v", tool, u, want, got)
			}
			if r.calls != 0 {
				t.Errorf("%s %q must not touch yt-dlp, calls = %d", tool, u, r.calls)
			}
		}
	}
}

// cancelledContext makes the real fetcher fail fast, so tests can observe
// that a request reached it without any network access.
func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestExtractStructuredIgnoresYouTubeDelegation(t *testing.T) {
	r := &ytFakeRunner{}
	s := newYouTubeTestServer(t, r)
	s.fetcherService = fetch.NewFetcherService(1000)
	callToolCtx(t, cancelledContext(), s, "extract_structured", map[string]interface{}{"url": ytTestURL})
	if r.calls != 0 {
		t.Fatalf("extract_structured must not delegate, calls = %d", r.calls)
	}
}

func TestYouTubeServiceNilKeepsPreviousBehavior(t *testing.T) {
	s := newYouTubeTestServer(t, nil)
	s.fetcherService = fetch.NewFetcherService(1000)

	msg := errorMessage(t, callTool(t, s, "fetch_youtube_transcript", map[string]interface{}{"url": ytTestURL}))
	if !strings.Contains(msg, "youtube_unavailable") {
		t.Fatalf("error = %q, want youtube_unavailable", msg)
	}

	// Without a service a YouTube URL takes the old fetcher path, which
	// fails here on the cancelled context rather than on a youtube_* error.
	for _, tool := range []string{"fetch_url", "fetch_and_extract"} {
		resp := callToolCtx(t, cancelledContext(), s, tool, map[string]interface{}{"url": ytTestURL})
		if strings.Contains(errorMessage(t, resp), "youtube_") {
			t.Fatalf("%s reached the YouTube path without a service: %v", tool, resp)
		}
	}
}
