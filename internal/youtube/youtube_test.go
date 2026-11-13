package youtube

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
	"time"
)

// fakeRunner simulates yt-dlp by writing the files the real binary would
// write into its working directory.
type fakeRunner struct {
	mu       sync.Mutex
	calls    [][]string
	info     map[string]interface{}
	vtt      string
	failCall int // 1-based call that fails; 0 never
	stderr   string
}

func (f *fakeRunner) Run(ctx context.Context, dir, name string, args []string) ([]byte, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string{name}, args...))
	n := len(f.calls)
	if f.failCall == n {
		return nil, []byte(f.stderr), errors.New("exit status 1")
	}
	output := ""
	for i, a := range args {
		if a == "-o" && i+1 < len(args) {
			output = args[i+1]
		}
	}
	base := strings.TrimSuffix(output, ".%(ext)s")
	if hasArg(args, "--write-info-json") {
		b, _ := json.Marshal(f.info)
		if err := os.WriteFile(filepath.Join(dir, base+".info.json"), b, 0o600); err != nil {
			return nil, nil, err
		}
		return nil, nil, nil
	}
	lang := argValue(args, "--sub-langs")
	if err := os.WriteFile(filepath.Join(dir, base+"."+lang+".vtt"), []byte(f.vtt), 0o600); err != nil {
		return nil, nil, err
	}
	return nil, nil, nil
}

func hasArg(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func argValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func sampleInfo() map[string]interface{} {
	return map[string]interface{}{
		"id":          "Q-_VdeexbUI",
		"title":       "Un video de prueba",
		"channel":     "Canal Ejemplo",
		"uploader":    "Uploader Ejemplo",
		"upload_date": "20250102",
		"duration":    3725.0,
		"language":    "es",
		"subtitles":   map[string]interface{}{"live_chat": []interface{}{map[string]interface{}{"ext": "json"}}},
		"automatic_captions": map[string]interface{}{
			"ar-orig": []interface{}{map[string]interface{}{"ext": "vtt"}},
			"en-orig": []interface{}{map[string]interface{}{"ext": "vtt"}},
			"es-orig": []interface{}{map[string]interface{}{"ext": "vtt"}},
			"pt-orig": []interface{}{map[string]interface{}{"ext": "vtt"}},
			"en":      []interface{}{map[string]interface{}{"ext": "vtt"}},
		},
	}
}

const sampleVTT = "WEBVTT\n\n00:00:00.000 --> 00:00:02.000\nhola<00:00:01.000><c> mundo</c>\n\n00:00:02.000 --> 00:00:02.010\nhola mundo\n\n00:00:02.010 --> 00:00:04.000\nhola mundo\nsegunda línea\n"

func newTestService(r Runner, now func() time.Time) *Service {
	s := NewService(Config{Binary: "yt-dlp", JSRuntimes: "node", POTBaseURL: "http://127.0.0.1:4416"}, r)
	if now != nil {
		s.now = now
	}
	return s
}

func TestFetchTwoCallsAndCache(t *testing.T) {
	r := &fakeRunner{info: sampleInfo(), vtt: sampleVTT}
	s := newTestService(r, nil)
	ctx := context.Background()

	got, err := s.Fetch(ctx, Request{URL: "https://youtu.be/Q-_VdeexbUI"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(r.calls) != 2 {
		t.Fatalf("yt-dlp calls = %d, want exactly 2: %v", len(r.calls), r.calls)
	}
	common := []string{"--ignore-config", "--force-ipv4", "--no-playlist", "--sleep-requests", "1", "--js-runtimes", "node",
		"--extractor-args", "youtubepot-bgutilhttp:base_url=http://127.0.0.1:4416",
		"--extractor-args", "youtube:fetch_pot=always"}
	for i, call := range r.calls {
		joined := strings.Join(call, "\x00")
		for _, flag := range common {
			if !strings.Contains(joined, flag) {
				t.Errorf("call %d missing %q: %v", i+1, flag, call)
			}
		}
		if !hasArg(call, "--skip-download") {
			t.Errorf("call %d must pass --skip-download: %v", i+1, call)
		}
		if call[len(call)-2] != "--" {
			t.Errorf("call %d must end the options with -- before the URL: %v", i+1, call)
		}
	}
	first, second := r.calls[0], r.calls[1]
	if !hasArg(first, "--write-info-json") || last(first) != "https://www.youtube.com/watch?v=Q-_VdeexbUI" {
		t.Errorf("first call must fetch metadata for the canonical URL: %v", first)
	}
	if !hasArg(second, "--load-info-json") || !hasArg(second, "--write-auto-subs") || hasArg(second, "--write-subs") {
		t.Errorf("second call must load the info JSON and request automatic subs only: %v", second)
	}
	if v := argValue(second, "--sub-langs"); v != "es-orig" {
		t.Errorf("--sub-langs = %q, want the single track es-orig", v)
	}
	if v := argValue(second, "--sub-format"); v != "vtt" {
		t.Errorf("--sub-format = %q, want vtt", v)
	}

	want := Transcript{
		URL:        "https://www.youtube.com/watch?v=Q-_VdeexbUI",
		VideoID:    "Q-_VdeexbUI",
		Title:      "Un video de prueba",
		Channel:    "Canal Ejemplo",
		UploadDate: "2025-01-02",
		Duration:   "1:02:05",
		DurationS:  3725,
		Language:   "es-orig",
		TrackType:  "automatic",
		Cached:     false,
		TotalChars: len([]rune("hola mundo\nsegunda línea")),
		Transcript: "hola mundo\nsegunda línea",
		Source:     "yt-dlp",
	}
	if got.Warnings != nil {
		t.Errorf("unexpected warnings: %v", got.Warnings)
	}
	got.Warnings = nil
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("transcript mismatch\n got: %+v\nwant: %+v", *got, want)
	}

	again, err := s.Fetch(ctx, Request{URL: "https://www.youtube.com/watch?v=Q-_VdeexbUI"})
	if err != nil {
		t.Fatalf("second Fetch: %v", err)
	}
	if len(r.calls) != 2 {
		t.Fatalf("cached video must not call yt-dlp again; calls = %d", len(r.calls))
	}
	if !again.Cached || again.Transcript != want.Transcript {
		t.Fatalf("second fetch must come from cache: %+v", again)
	}

	if _, err := s.Fetch(ctx, Request{URL: "Q-_VdeexbUI", Language: "en"}); err != nil {
		t.Fatalf("other language Fetch: %v", err)
	}
	if len(r.calls) != 4 {
		t.Fatalf("a different language is a different cache key; calls = %d", len(r.calls))
	}
	if v := argValue(r.calls[3], "--sub-langs"); v != "en-orig" {
		t.Errorf("requested en must pick en-orig, got %q", v)
	}
}

func last(s []string) string { return s[len(s)-1] }

func TestFetchManualTrackUsesWriteSubs(t *testing.T) {
	info := sampleInfo()
	info["subtitles"] = map[string]interface{}{"es": []interface{}{map[string]interface{}{"ext": "vtt"}}}
	r := &fakeRunner{info: info, vtt: sampleVTT}
	got, err := newTestService(r, nil).Fetch(context.Background(), Request{URL: "https://youtu.be/Q-_VdeexbUI"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	second := r.calls[1]
	if !hasArg(second, "--write-subs") || hasArg(second, "--write-auto-subs") {
		t.Fatalf("manual track must use --write-subs only: %v", second)
	}
	if got.TrackType != "manual" || got.Language != "es" {
		t.Fatalf("got %s/%s, want manual/es", got.TrackType, got.Language)
	}
}

func TestFetchTruncatesWithNote(t *testing.T) {
	r := &fakeRunner{info: sampleInfo(), vtt: sampleVTT}
	s := newTestService(r, nil)
	got, err := s.Fetch(context.Background(), Request{URL: "https://youtu.be/Q-_VdeexbUI", MaxChars: 10})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !got.Truncated || !strings.HasPrefix(got.Transcript, "hola mundo") || !strings.Contains(got.Transcript, "[transcript truncated: 10 of 24 characters]") {
		t.Fatalf("truncation note missing: %+v", got)
	}
	full, err := s.Fetch(context.Background(), Request{URL: "https://youtu.be/Q-_VdeexbUI"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if full.Truncated || full.Transcript != "hola mundo\nsegunda línea" || !full.Cached {
		t.Fatalf("cache must keep the full transcript: %+v", full)
	}
}

func TestFetchBlockedIsOneErrorAndCoolsDown(t *testing.T) {
	for _, stderr := range []string{
		"ERROR: [youtube] Q-_VdeexbUI: Unable to download API page: HTTP Error 429: Too Many Requests",
		"ERROR: [youtube] Q-_VdeexbUI: Sign in to confirm you’re not a bot. Use --cookies-from-browser",
		"ERROR: [youtube] Q-_VdeexbUI: Sign in to confirm you're not a bot",
		"ERROR: unable to download webpage: redirected to https://www.google.com/sorry/index?continue=...",
	} {
		t.Run(stderr[:40], func(t *testing.T) {
			clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			r := &fakeRunner{info: sampleInfo(), vtt: sampleVTT, failCall: 1, stderr: stderr}
			s := newTestService(r, func() time.Time { return clock })
			_, err := s.Fetch(context.Background(), Request{URL: "https://youtu.be/Q-_VdeexbUI"})
			var blocked *BlockedError
			if !errors.As(err, &blocked) {
				t.Fatalf("want BlockedError, got %v", err)
			}
			if len(r.calls) != 1 {
				t.Fatalf("a block must not be retried; calls = %d", len(r.calls))
			}
			msg := err.Error()
			if !strings.Contains(msg, "youtube_blocked") || !strings.Contains(msg, "do not retry") {
				t.Fatalf("error must explain the block: %q", msg)
			}

			// Cooldown: the next call fails fast without touching YouTube.
			r.failCall = 0
			clock = clock.Add(time.Minute)
			if _, err := s.Fetch(context.Background(), Request{URL: "https://youtu.be/dQw4w9WgXcQ"}); !errors.As(err, &blocked) {
				t.Fatalf("during cooldown want BlockedError, got %v", err)
			}
			if len(r.calls) != 1 {
				t.Fatalf("cooldown must not call yt-dlp; calls = %d", len(r.calls))
			}

			// After the cooldown the failure was not cached.
			clock = clock.Add(DefaultBlockCooldown)
			if _, err := s.Fetch(context.Background(), Request{URL: "https://youtu.be/Q-_VdeexbUI"}); err != nil {
				t.Fatalf("after cooldown: %v", err)
			}
			if len(r.calls) != 3 {
				t.Fatalf("after cooldown want 2 new calls, total %d", len(r.calls))
			}
		})
	}
}

func TestFetchBlockedOnSecondCall(t *testing.T) {
	r := &fakeRunner{info: sampleInfo(), vtt: sampleVTT, failCall: 2, stderr: "ERROR: Unable to download video subtitles for 'es-orig': HTTP Error 429: Too Many Requests"}
	_, err := newTestService(r, nil).Fetch(context.Background(), Request{URL: "https://youtu.be/Q-_VdeexbUI"})
	var blocked *BlockedError
	if !errors.As(err, &blocked) || len(r.calls) != 2 {
		t.Fatalf("want BlockedError after exactly 2 calls, got %v after %d", err, len(r.calls))
	}
}

func TestFetchRejectsInvalidInput(t *testing.T) {
	r := &fakeRunner{info: sampleInfo(), vtt: sampleVTT}
	s := newTestService(r, nil)
	if _, err := s.Fetch(context.Background(), Request{URL: "https://www.youtube.com/@channel"}); !errors.Is(err, ErrNotVideo) {
		t.Fatalf("channel URL: want ErrNotVideo, got %v", err)
	}
	for _, lang := range []string{"es;rm", "--exec", "e", "es.*", "x-" + strings.Repeat("a", 20)} {
		if _, err := s.Fetch(context.Background(), Request{URL: "https://youtu.be/Q-_VdeexbUI", Language: lang}); !errors.Is(err, ErrInvalidLanguage) {
			t.Errorf("language %q: want ErrInvalidLanguage, got %v", lang, err)
		}
	}
	if len(r.calls) != 0 {
		t.Fatalf("invalid input must not call yt-dlp; calls = %d", len(r.calls))
	}
}

func TestFetchNoSubtitles(t *testing.T) {
	info := sampleInfo()
	info["automatic_captions"] = map[string]interface{}{}
	r := &fakeRunner{info: info, vtt: sampleVTT}
	_, err := newTestService(r, nil).Fetch(context.Background(), Request{URL: "https://youtu.be/Q-_VdeexbUI"})
	if !errors.Is(err, ErrNoSubtitles) || len(r.calls) != 1 {
		t.Fatalf("want ErrNoSubtitles after the metadata call only, got %v after %d calls", err, len(r.calls))
	}
}

func TestFetchGenericFailureIsNotBlock(t *testing.T) {
	r := &fakeRunner{info: sampleInfo(), vtt: sampleVTT, failCall: 1, stderr: "ERROR: [youtube] Q-_VdeexbUI: Video unavailable"}
	s := newTestService(r, nil)
	_, err := s.Fetch(context.Background(), Request{URL: "https://youtu.be/Q-_VdeexbUI"})
	var blocked *BlockedError
	if err == nil || errors.As(err, &blocked) || !strings.Contains(err.Error(), "Video unavailable") {
		t.Fatalf("want a plain error with yt-dlp's reason, got %v", err)
	}
	r.failCall = 0
	if _, err := s.Fetch(context.Background(), Request{URL: "https://youtu.be/Q-_VdeexbUI"}); err != nil {
		t.Fatalf("a generic failure must not start a cooldown: %v", err)
	}
}

func TestFetchPOTPolicy(t *testing.T) {
	for _, tc := range []struct{ configured, want string }{
		{"", "always"},
		{"always", "always"},
		{"auto", "auto"},
		{"never", "never"},
		{"bogus;rm", "always"},
	} {
		r := &fakeRunner{info: sampleInfo(), vtt: sampleVTT}
		s := NewService(Config{FetchPOT: tc.configured}, r)
		if _, err := s.Fetch(context.Background(), Request{URL: "https://youtu.be/Q-_VdeexbUI"}); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		for i, call := range r.calls {
			if !strings.Contains(strings.Join(call, "\x00"), "--extractor-args\x00youtube:fetch_pot="+tc.want) {
				t.Errorf("FetchPOT %q: call %d lacks youtube:fetch_pot=%s: %v", tc.configured, i+1, tc.want, call)
			}
		}
	}
}

func TestFetchDefaultLanguage(t *testing.T) {
	const url = "https://youtu.be/Q-_VdeexbUI"
	arInfo := func() map[string]interface{} {
		info := sampleInfo()
		info["language"] = "ar"
		return info
	}
	for _, tc := range []struct {
		name, defaultLang, reqLang, want string
	}{
		{"default applies when request omits language", "es", "", "es-orig"},
		{"explicit language wins over default", "es", "en", "en-orig"},
		{"invalid default is ignored", "es;rm", "", "ar-orig"},
		{"no default falls back to spoken language", "", "", "ar-orig"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeRunner{info: arInfo(), vtt: sampleVTT}
			s := NewService(Config{DefaultLanguage: tc.defaultLang}, r)
			if _, err := s.Fetch(context.Background(), Request{URL: url, Language: tc.reqLang}); err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if len(r.calls) != 2 {
				t.Fatalf("calls = %d, want 2", len(r.calls))
			}
			if got := argValue(r.calls[1], "--sub-langs"); got != tc.want {
				t.Errorf("--sub-langs = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFetchDefaultLanguageSharesCacheKeyWithExplicit(t *testing.T) {
	r := &fakeRunner{info: sampleInfo(), vtt: sampleVTT}
	s := NewService(Config{DefaultLanguage: "es"}, r)
	ctx := context.Background()
	if _, err := s.Fetch(ctx, Request{URL: "https://youtu.be/Q-_VdeexbUI"}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got, err := s.Fetch(ctx, Request{URL: "https://youtu.be/Q-_VdeexbUI", Language: "es"})
	if err != nil {
		t.Fatalf("Fetch explicit: %v", err)
	}
	if !got.Cached || len(r.calls) != 2 {
		t.Fatalf("explicit es must hit the default's cache entry; cached=%v calls=%d", got.Cached, len(r.calls))
	}
}
