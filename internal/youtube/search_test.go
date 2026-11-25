package youtube

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// searchRunner is a fake yt-dlp that prints a canned search document.
type searchRunner struct {
	mu     sync.Mutex
	calls  [][]string
	stdout string
	stderr string
	err    error
	block  bool // wait for the context instead of answering
}

func (r *searchRunner) Run(ctx context.Context, _, name string, args []string) ([]byte, []byte, error) {
	r.mu.Lock()
	r.calls = append(r.calls, append([]string{name}, args...))
	r.mu.Unlock()
	if r.block {
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}
	return []byte(r.stdout), []byte(r.stderr), r.err
}

const searchFixture = `{"_type":"playlist","id":"q","entries":[
 {"_type":"url","ie_key":"Youtube","id":"uiUCIz-3CWM","url":"https://www.youtube.com/watch?v=uiUCIz-3CWM","title":"Context tutorial","description":"Code:\n  https://example.test   more","duration":738,"channel":"Melkey","uploader":"Mel","view_count":60139,"live_status":null},
 {"_type":"url","ie_key":"Youtube","id":"abcdefghijk","title":"No url, uploader only, 1h","duration":3725.4,"uploader":"Someone","view_count":null,"timestamp":1700000000},
 {"_type":"url","ie_key":"Youtube","id":"upload_dat1","title":"Dated","upload_date":"20240229"},
 {"_type":"url","ie_key":"Youtube","id":"","title":"no id"},
 {"_type":"url","ie_key":"Youtube","id":"notitle0001","title":"  "},
 {"_type":"url","ie_key":"Youtube","id":"livelive001","title":"Live now","is_live":true,"live_status":"is_live"},
 {"_type":"url","ie_key":"Youtube","id":"upcoming001","title":"Soon","live_status":"is_upcoming"},
 {"_type":"url","ie_key":"YoutubeTab","id":"UC1Zfv1Zrp1q5lKgBomzOyCA","title":"A channel","url":"https://www.youtube.com/channel/UC1Zfv1Zrp1q5lKgBomzOyCA"},
 {"_type":"playlist","id":"PLxxxxxxxxxx","title":"A playlist","url":"https://www.youtube.com/playlist?list=PLxxxxxxxxxx"},
 "not an object",
 {"_type":"url","ie_key":"Youtube","id":"wasliveXXXX","title":"Was live","live_status":"was_live","duration":42900}
]}`

func newSearchService(r Runner) *Service {
	return NewService(Config{Binary: "/opt/yt-dlp", WorkDir: ""}, r)
}

func TestSearchRequestArgv(t *testing.T) {
	r := &searchRunner{stdout: searchFixture}
	if _, err := newSearchService(r).Search(context.Background(), "go -x; $(rm) 'q'", 12); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("yt-dlp calls = %d, want 1", len(r.calls))
	}
	call := r.calls[0]
	if call[0] != "/opt/yt-dlp" {
		t.Errorf("binary = %q, want the configured one", call[0])
	}
	args := call[1:]
	for _, want := range []string{"--ignore-config", "--flat-playlist", "--dump-single-json"} {
		if !hasArg(args, want) {
			t.Errorf("argv %q lacks %s", args, want)
		}
	}
	n := len(args)
	if args[n-2] != "--" || args[n-1] != "ytsearch12:go -x; $(rm) 'q'" {
		t.Errorf("tail of argv = %q, want [-- ytsearch12:<query>] as one element", args[n-2:])
	}
	if hasArg(args, "--write-info-json") || hasArg(args, "--write-subs") {
		t.Errorf("a search must not write files: %q", args)
	}
}

func TestSearchCapsLimit(t *testing.T) {
	for limit, want := range map[int]string{0: "ytsearch10:q", -3: "ytsearch10:q", 7: "ytsearch7:q", 50: "ytsearch50:q", 51: "ytsearch50:q", 999: "ytsearch50:q"} {
		r := &searchRunner{stdout: searchFixture}
		if _, err := newSearchService(r).Search(context.Background(), "q", limit); err != nil {
			t.Fatal(err)
		}
		args := r.calls[0]
		if got := args[len(args)-1]; got != want {
			t.Errorf("limit %d: search term = %q, want %q", limit, got, want)
		}
	}
}

func TestSearchMapsAndSkipsEntries(t *testing.T) {
	r := &searchRunner{stdout: searchFixture}
	got, err := newSearchService(r).Search(context.Background(), "q", 50)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range got {
		ids = append(ids, e.ID)
	}
	if want := "uiUCIz-3CWM abcdefghijk upload_dat1 wasliveXXXX"; strings.Join(ids, " ") != want {
		t.Fatalf("kept ids = %q, want %q", strings.Join(ids, " "), want)
	}
	first := got[0]
	if first.URL != "https://www.youtube.com/watch?v=uiUCIz-3CWM" || first.Title != "Context tutorial" ||
		first.Channel != "Melkey" || first.DurationSeconds != 738 || first.Views != 60139 {
		t.Errorf("first entry = %+v", first)
	}
	if first.Description != "Code: https://example.test more" {
		t.Errorf("description = %q, want whitespace collapsed", first.Description)
	}
	second := got[1]
	if second.URL != "https://www.youtube.com/watch?v=abcdefghijk" {
		t.Errorf("missing url must become the watch URL, got %q", second.URL)
	}
	if second.Channel != "Someone" || second.Views != 0 || second.DurationSeconds != 3725 {
		t.Errorf("second entry = %+v", second)
	}
	if want := time.Unix(1700000000, 0).UTC(); !second.PublishedAt.Equal(want) {
		t.Errorf("publishedAt = %v, want %v", second.PublishedAt, want)
	}
	if want := time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC); !got[2].PublishedAt.Equal(want) {
		t.Errorf("upload_date publishedAt = %v, want %v", got[2].PublishedAt, want)
	}
	if !got[0].PublishedAt.IsZero() {
		t.Errorf("no date must stay zero, got %v", got[0].PublishedAt)
	}
}

func TestSearchKeepsAtMostLimitEntries(t *testing.T) {
	got, err := newSearchService(&searchRunner{stdout: searchFixture}).Search(context.Background(), "q", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("entries = %d, want 2", len(got))
	}
}

func TestSearchErrors(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name   string
		runner *searchRunner
		want   string
	}{
		{"exit error", &searchRunner{err: errors.New("exit status 1"), stderr: "ERROR: boom"}, "youtube_error: yt-dlp failed: ERROR: boom"},
		{"not installed", &searchRunner{err: exec.ErrNotFound}, "youtube_unavailable"},
		{"invalid json", &searchRunner{stdout: "{not json"}, "youtube_error: invalid yt-dlp search output"},
		{"empty stdout", &searchRunner{}, "youtube_error: invalid yt-dlp search output"},
		{"no entries", &searchRunner{stdout: `{"_type":"playlist","entries":[]}`}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := newSearchService(tc.runner).Search(ctx, "q", 5)
			if tc.want == "" {
				if err != nil || len(got) != 0 {
					t.Fatalf("got %v, %v; want no entries and no error", got, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestSearchRejectsEmptyQueryWithoutRunning(t *testing.T) {
	r := &searchRunner{stdout: searchFixture}
	if _, err := newSearchService(r).Search(context.Background(), "  \n ", 5); err == nil {
		t.Fatal("empty query must fail")
	}
	if len(r.calls) != 0 {
		t.Errorf("yt-dlp ran for an empty query")
	}
}

func TestSearchNilServiceIsUnavailable(t *testing.T) {
	var s *Service
	if _, err := s.Search(context.Background(), "q", 5); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
}

func TestSearchTimeout(t *testing.T) {
	s := NewService(Config{Timeout: 50 * time.Millisecond}, &searchRunner{block: true})
	start := time.Now()
	_, err := s.Search(context.Background(), "q", 5)
	if err == nil || !strings.Contains(err.Error(), "youtube_timeout") {
		t.Fatalf("error = %v, want youtube_timeout", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("search did not honor the timeout")
	}
}

func TestSearchBlockStartsCooldownAndSkipsYtDlp(t *testing.T) {
	r := &searchRunner{err: errors.New("exit status 1"), stderr: "ERROR: HTTP Error 429: Too Many Requests"}
	s := newSearchService(r)
	_, err := s.Search(context.Background(), "q", 5)
	var blocked *BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("error = %v, want BlockedError", err)
	}
	if _, err := s.Search(context.Background(), "q2", 5); !errors.As(err, &blocked) {
		t.Fatalf("second error = %v, want BlockedError from the cooldown", err)
	}
	if len(r.calls) != 1 {
		t.Errorf("yt-dlp calls = %d, want 1: the cooldown must stop the second one", len(r.calls))
	}
}

func TestExecRunnerKeepsLargeSearchOutput(t *testing.T) {
	// 50 flat entries are about 60 KB; the capture limit for stdout must
	// leave room for them (transcript calls print nothing).
	out, _, err := ExecRunner{}.Run(context.Background(), t.TempDir(), "/bin/sh", []string{"-c", "head -c 300000 /dev/zero | tr '\\0' x"})
	if err != nil {
		t.Skipf("no /bin/sh: %v", err)
	}
	if len(out) != 300000 {
		t.Errorf("captured %d bytes, want 300000", len(out))
	}
}
