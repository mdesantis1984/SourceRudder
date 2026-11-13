package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultCacheTTL keeps transcripts for a week: they rarely change and
	// every cache miss costs two YouTube requests.
	DefaultCacheTTL = 7 * 24 * time.Hour
	// DefaultBlockCooldown is how long the service refuses to call YouTube
	// after a 429 or bot check.
	DefaultBlockCooldown = 15 * time.Minute
	// DefaultTimeout bounds each yt-dlp invocation.
	DefaultTimeout = 2 * time.Minute
	// DefaultMaxChars is the transcript size returned when the caller does
	// not choose one.
	DefaultMaxChars = 60000
	// MaxMaxChars caps maxChars so a single response stays reasonable.
	MaxMaxChars = 400000

	defaultCacheEntries = 256
	maxCapturedOutput   = 64 << 10
	outputBase          = "video"
)

var (
	// ErrNotVideo means the input is not a YouTube video URL or ID.
	ErrNotVideo = errors.New("youtube_not_video: not a YouTube video URL or 11-character video ID")
	// ErrInvalidLanguage means the language code failed validation.
	ErrInvalidLanguage = errors.New("youtube_invalid_language: language must be a code like 'es', 'en' or 'pt-BR'")
	// ErrNoSubtitles means the video exposes no usable subtitle track.
	ErrNoSubtitles = errors.New("youtube_no_transcript: the video has no manual subtitles or original-language automatic captions")
	// ErrUnavailable means the service is not configured (no yt-dlp).
	ErrUnavailable = errors.New("youtube_unavailable: transcript support is not configured on this server")
)

// BlockedError reports that YouTube rate-limited or bot-checked this
// server's IP. Callers must not retry before RetryAfter.
type BlockedError struct {
	RetryAfter time.Duration
	Detail     string
}

func (e *BlockedError) Error() string {
	minutes := int(e.RetryAfter.Round(time.Minute) / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	msg := fmt.Sprintf("youtube_blocked: YouTube blocked this server's IP (HTTP 429 or bot check); do not retry now, wait at least %d minute(s) and do not try other fetch tools for this video", minutes)
	if e.Detail != "" {
		msg += " (" + e.Detail + ")"
	}
	return msg
}

var blockPatterns = []string{
	"http error 429",
	"too many requests",
	"confirm you're not a bot",
	"confirm you’re not a bot",
	"google.com/sorry",
}

// Runner executes a program with an argument array (never a shell) in dir
// and returns its stdout and stderr.
type Runner interface {
	Run(ctx context.Context, dir, name string, args []string) (stdout, stderr []byte, err error)
}

// ExecRunner runs real processes.
type ExecRunner struct{}

// Run implements Runner.
func (ExecRunner) Run(ctx context.Context, dir, name string, args []string) ([]byte, []byte, error) {
	// The binary is operator-configured (YTDLP_PATH) and receives an
	// argument array; no shell is involved.
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- configured yt-dlp binary, argv only
	cmd.Dir = dir
	var stdout, stderr limitedBuffer
	stdout.limit, stderr.limit = maxCapturedOutput, maxCapturedOutput
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.Len(); room > 0 {
		if len(p) > room {
			b.Buffer.Write(p[:room])
		} else {
			b.Buffer.Write(p)
		}
	}
	return len(p), nil
}

// Config configures the transcript service.
type Config struct {
	// Binary is the yt-dlp executable (path or name in PATH).
	Binary string
	// JSRuntimes is passed as --js-runtimes (e.g. "node"); empty lets
	// yt-dlp pick its default.
	JSRuntimes string
	// POTBaseURL points the bgutil PO token plugin at its HTTP server;
	// empty keeps the plugin default (http://127.0.0.1:4416).
	POTBaseURL string
	// FetchPOT is yt-dlp's youtube:fetch_pot policy (always, auto or
	// never). The default "always" attaches a PO token to the player
	// request too: with "auto" yt-dlp only asks for one when it is
	// required, and a flagged IP gets the bot check before that happens.
	FetchPOT string
	// DefaultLanguage is the language requested when a call omits one
	// (fetch_url has no language argument). It is validated like an
	// explicit language and ignored when invalid.
	DefaultLanguage string
	// WorkDir is where per-request scratch directories are created;
	// empty uses os.TempDir().
	WorkDir       string
	CacheTTL      time.Duration
	BlockCooldown time.Duration
	Timeout       time.Duration
	MaxEntries    int
}

// Request is one transcript request.
type Request struct {
	URL      string
	Language string
	MaxChars int
}

// Transcript is the tool response.
type Transcript struct {
	URL        string   `json:"url"`
	VideoID    string   `json:"videoId"`
	Title      string   `json:"title"`
	Channel    string   `json:"channel"`
	UploadDate string   `json:"uploadDate,omitempty"`
	Duration   string   `json:"duration,omitempty"`
	DurationS  int      `json:"durationSeconds,omitempty"`
	Language   string   `json:"language"`
	TrackType  string   `json:"trackType"`
	Cached     bool     `json:"cached"`
	Truncated  bool     `json:"truncated"`
	TotalChars int      `json:"totalChars"`
	Transcript string   `json:"transcript"`
	Source     string   `json:"source"`
	Warnings   []string `json:"warnings,omitempty"`
}

type cacheEntry struct {
	value   Transcript
	expires time.Time
}

// Service fetches and caches transcripts.
type Service struct {
	cfg    Config
	runner Runner
	now    func() time.Time

	mu            sync.Mutex
	cache         map[string]cacheEntry
	order         []string
	blockedUntil  time.Time
	blockedDetail string

	// run serializes yt-dlp work so concurrent requests never multiply
	// the request rate against YouTube.
	run sync.Mutex
}

// NewService builds a Service. A nil runner uses ExecRunner.
func NewService(cfg Config, runner Runner) *Service {
	if cfg.Binary == "" {
		cfg.Binary = "yt-dlp"
	}
	switch cfg.FetchPOT {
	case "always", "auto", "never":
	default:
		cfg.FetchPOT = "always"
	}
	cfg.DefaultLanguage = strings.TrimSpace(cfg.DefaultLanguage)
	if !languagePattern.MatchString(cfg.DefaultLanguage) {
		cfg.DefaultLanguage = ""
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = DefaultCacheTTL
	}
	if cfg.BlockCooldown <= 0 {
		cfg.BlockCooldown = DefaultBlockCooldown
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = defaultCacheEntries
	}
	if runner == nil {
		runner = ExecRunner{}
	}
	return &Service{cfg: cfg, runner: runner, now: time.Now, cache: map[string]cacheEntry{}}
}

// Fetch returns the transcript for req, from cache when possible.
func (s *Service) Fetch(ctx context.Context, req Request) (*Transcript, error) {
	if s == nil {
		return nil, ErrUnavailable
	}
	id, ok := ParseVideoID(req.URL)
	if !ok {
		return nil, ErrNotVideo
	}
	lang := strings.TrimSpace(req.Language)
	if lang != "" && !languagePattern.MatchString(lang) {
		return nil, ErrInvalidLanguage
	}
	if lang == "" {
		lang = s.cfg.DefaultLanguage
	}
	maxChars := req.MaxChars
	if maxChars <= 0 {
		maxChars = DefaultMaxChars
	}
	if maxChars > MaxMaxChars {
		maxChars = MaxMaxChars
	}
	key := id + "|" + strings.ToLower(lang)

	if t, ok := s.cached(key); ok {
		t.Cached = true
		return truncate(t, maxChars), nil
	}

	s.run.Lock()
	defer s.run.Unlock()
	// Another request may have filled the cache while this one waited.
	if t, ok := s.cached(key); ok {
		t.Cached = true
		return truncate(t, maxChars), nil
	}
	if err := s.checkCooldown(); err != nil {
		return nil, err
	}

	t, err := s.fetchUncached(ctx, id, lang)
	if err != nil {
		var blocked *BlockedError
		if errors.As(err, &blocked) {
			s.startCooldown(blocked.Detail)
			blocked.RetryAfter = s.cfg.BlockCooldown
		}
		return nil, err
	}
	s.store(key, *t)
	return truncate(*t, maxChars), nil
}

func (s *Service) cached(key string) (Transcript, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.cache[key]
	if !ok || !s.now().Before(e.expires) {
		return Transcript{}, false
	}
	return e.value, true
}

func (s *Service) store(key string, t Transcript) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.cache[key]; !exists {
		s.order = append(s.order, key)
	}
	s.cache[key] = cacheEntry{value: t, expires: s.now().Add(s.cfg.CacheTTL)}
	for len(s.order) > s.cfg.MaxEntries {
		delete(s.cache, s.order[0])
		s.order = s.order[1:]
	}
}

func (s *Service) checkCooldown() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if remaining := s.blockedUntil.Sub(s.now()); remaining > 0 {
		return &BlockedError{RetryAfter: remaining, Detail: "cooldown after an earlier block: " + s.blockedDetail}
	}
	return nil
}

func (s *Service) startCooldown(detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blockedUntil = s.now().Add(s.cfg.BlockCooldown)
	s.blockedDetail = detail
}

func (s *Service) commonArgs() []string {
	args := []string{
		"--ignore-config", "--force-ipv4", "--no-playlist", "--sleep-requests", "1",
		"--skip-download", "--no-progress", "--no-cache-dir",
	}
	if s.cfg.JSRuntimes != "" {
		args = append(args, "--js-runtimes", s.cfg.JSRuntimes)
	}
	if s.cfg.POTBaseURL != "" {
		args = append(args, "--extractor-args", "youtubepot-bgutilhttp:base_url="+s.cfg.POTBaseURL)
	}
	args = append(args, "--extractor-args", "youtube:fetch_pot="+s.cfg.FetchPOT)
	return append(args, "-o", outputBase+".%(ext)s")
}

func (s *Service) fetchUncached(ctx context.Context, id, lang string) (*Transcript, error) {
	dir, err := os.MkdirTemp(s.cfg.WorkDir, "sourcerudder-yt-")
	if err != nil {
		return nil, fmt.Errorf("youtube_error: create scratch dir: %w", err)
	}
	defer os.RemoveAll(dir)
	// Every file yt-dlp writes is read through a root confined to the
	// scratch directory.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("youtube_error: open scratch dir: %w", err)
	}
	defer root.Close()

	// Call 1: metadata only.
	metaArgs := append(s.commonArgs(), "--write-info-json", "--", CanonicalURL(id))
	if err := s.invoke(ctx, dir, metaArgs); err != nil {
		return nil, err
	}
	infoPath := filepath.Join(dir, outputBase+".info.json")
	raw, err := root.ReadFile(outputBase + ".info.json")
	if err != nil {
		return nil, fmt.Errorf("youtube_error: yt-dlp wrote no metadata: %w", err)
	}
	var info videoInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, fmt.Errorf("youtube_error: invalid yt-dlp metadata: %w", err)
	}

	track, ok := chooseTrack(&info, lang)
	if !ok {
		return nil, ErrNoSubtitles
	}

	// Call 2: exactly one subtitle track, reusing the metadata.
	subFlag := "--write-auto-subs"
	if track.Manual {
		subFlag = "--write-subs"
	}
	subArgs := append(s.commonArgs(), "--load-info-json", infoPath, subFlag,
		"--sub-langs", track.Lang, "--sub-format", "vtt", "--", CanonicalURL(id))
	if err := s.invoke(ctx, dir, subArgs); err != nil {
		return nil, err
	}
	matches, _ := fs.Glob(root.FS(), outputBase+".*.vtt")
	if len(matches) == 0 {
		return nil, fmt.Errorf("youtube_error: yt-dlp wrote no subtitle file for track %q", track.Lang)
	}
	vtt, err := root.ReadFile(matches[0])
	if err != nil {
		return nil, fmt.Errorf("youtube_error: read subtitles: %w", err)
	}
	text := VTTToText(string(vtt))
	if text == "" {
		return nil, ErrNoSubtitles
	}

	t := &Transcript{
		URL:        CanonicalURL(id),
		VideoID:    id,
		Title:      info.Title,
		Channel:    firstNonEmpty(info.Channel, info.Uploader),
		UploadDate: formatDate(info.UploadDate),
		DurationS:  int(info.Duration),
		Duration:   formatDuration(int(info.Duration)),
		Language:   track.Lang,
		TrackType:  "automatic",
		TotalChars: len([]rune(text)),
		Transcript: text,
		Source:     "yt-dlp",
	}
	if track.Manual {
		t.TrackType = "manual"
	}
	if info.ID != "" && info.ID != id {
		t.Warnings = append(t.Warnings, "yt-dlp reported video id "+info.ID)
	}
	return t, nil
}

func (s *Service) invoke(ctx context.Context, dir string, args []string) error {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	stdout, stderr, err := s.runner.Run(ctx, dir, s.cfg.Binary, args)
	if err == nil {
		return nil
	}
	output := string(stderr) + "\n" + string(stdout)
	if detail, blocked := blockDetail(output); blocked {
		return &BlockedError{RetryAfter: s.cfg.BlockCooldown, Detail: detail}
	}
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%w: %s not found", ErrUnavailable, s.cfg.Binary)
	}
	if ctx.Err() != nil {
		return fmt.Errorf("youtube_timeout: yt-dlp did not finish within %s", s.cfg.Timeout)
	}
	return fmt.Errorf("youtube_error: yt-dlp failed: %s", lastErrorLine(output, err))
}

func blockDetail(output string) (string, bool) {
	lower := strings.ToLower(output)
	for _, p := range blockPatterns {
		if strings.Contains(lower, p) {
			return lastErrorLine(output, nil), true
		}
	}
	return "", false
}

func lastErrorLine(output string, err error) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "ERROR:") {
			return truncateRunes(line, 300)
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return truncateRunes(line, 300)
		}
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

func truncate(t Transcript, maxChars int) *Transcript {
	runes := []rune(t.Transcript)
	if len(runes) > maxChars {
		t.Transcript = string(runes[:maxChars]) +
			fmt.Sprintf("\n[transcript truncated: %d of %d characters]", maxChars, len(runes))
		t.Truncated = true
	}
	return &t
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func formatDate(yyyymmdd string) string {
	if len(yyyymmdd) != 8 {
		return yyyymmdd
	}
	return yyyymmdd[:4] + "-" + yyyymmdd[4:6] + "-" + yyyymmdd[6:]
}

func formatDuration(seconds int) string {
	if seconds <= 0 {
		return ""
	}
	h, m, sec := seconds/3600, seconds/60%60, seconds%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%d:%02d", m, sec)
}
