package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// DefaultSearchLimit is the number of entries requested when the caller
	// passes no limit.
	DefaultSearchLimit = 10
	// MaxSearchLimit caps ytsearchN so one search stays small and cheap.
	MaxSearchLimit = 50
	// SearchTimeout bounds one search run (capped by Config.Timeout): a
	// search is a single request, so it must not hold a caller for the
	// two minutes a transcript may take.
	SearchTimeout = 30 * time.Second

	maxSearchTitleRunes       = 300
	maxSearchDescriptionRunes = 400
)

// SearchEntry is one video returned by a YouTube search.
type SearchEntry struct {
	ID              string
	Title           string
	URL             string
	Channel         string
	Description     string
	DurationSeconds int
	Views           int64
	// PublishedAt is zero when yt-dlp gave no timestamp or upload date
	// (flat search results usually carry neither).
	PublishedAt time.Time
}

// Search searches YouTube directly with
// `yt-dlp --flat-playlist --dump-single-json ytsearchN:<query>`, the same
// way music.thiscloud.com.ar does, and returns at most limit videos in
// yt-dlp's order. The query travels as one argv element after `--`; no shell
// is involved. Channels, playlists, live and upcoming placeholders and
// entries without id or title are skipped.
func (s *Service) Search(ctx context.Context, query string, limit int) ([]SearchEntry, error) {
	if s == nil {
		return nil, ErrUnavailable
	}
	query = strings.Join(strings.Fields(query), " ")
	if query == "" {
		return nil, errors.New("youtube_error: empty search query")
	}
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	if limit > MaxSearchLimit {
		limit = MaxSearchLimit
	}
	if err := s.checkCooldown(); err != nil {
		return nil, err
	}
	args := []string{
		"--ignore-config", "--force-ipv4", "--no-progress", "--no-cache-dir", "--no-warnings",
		"--flat-playlist", "--dump-single-json",
		"--", fmt.Sprintf("ytsearch%d:%s", limit, query),
	}
	timeout := s.cfg.Timeout
	if timeout > SearchTimeout {
		timeout = SearchTimeout
	}
	stdout, err := s.invokeOutput(ctx, "", args, timeout)
	if err != nil {
		var blocked *BlockedError
		if errors.As(err, &blocked) {
			s.startCooldown(blocked.Detail)
			blocked.RetryAfter = s.cfg.BlockCooldown
		}
		return nil, err
	}
	return parseSearchOutput(stdout, limit)
}

func parseSearchOutput(stdout []byte, limit int) ([]SearchEntry, error) {
	var doc struct {
		Entries []json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(stdout, &doc); err != nil {
		return nil, fmt.Errorf("youtube_error: invalid yt-dlp search output: %w", err)
	}
	entries := make([]SearchEntry, 0, len(doc.Entries))
	for _, raw := range doc.Entries {
		if len(entries) >= limit {
			break
		}
		var obj map[string]any
		if json.Unmarshal(raw, &obj) != nil || obj == nil {
			continue
		}
		if e, ok := searchEntry(obj); ok {
			entries = append(entries, e)
		}
	}
	return entries, nil
}

func searchEntry(obj map[string]any) (SearchEntry, bool) {
	if t := jsonText(obj, "_type"); t != "" && t != "url" && t != "video" && t != "url_transparent" {
		return SearchEntry{}, false // playlist, channel, ...
	}
	if k := jsonText(obj, "ie_key"); k != "" && k != "Youtube" {
		return SearchEntry{}, false // YoutubeTab: channels and playlists
	}
	id, title := jsonText(obj, "id"), jsonText(obj, "title")
	if !videoIDPattern.MatchString(id) || title == "" {
		return SearchEntry{}, false
	}
	if status := jsonText(obj, "live_status"); status == "is_live" || status == "is_upcoming" {
		return SearchEntry{}, false
	}
	if live, _ := obj["is_live"].(bool); live {
		return SearchEntry{}, false
	}
	e := SearchEntry{
		ID:          id,
		Title:       truncateRunes(title, maxSearchTitleRunes),
		URL:         CanonicalURL(id),
		Channel:     firstNonEmpty(jsonText(obj, "channel"), jsonText(obj, "uploader")),
		Description: truncateRunes(strings.Join(strings.Fields(jsonText(obj, "description")), " "), maxSearchDescriptionRunes),
	}
	if d, ok := jsonNumber(obj, "duration"); ok && d > 0 && d < 1<<31 {
		e.DurationSeconds = int(d + 0.5)
	}
	if v, ok := jsonNumber(obj, "view_count"); ok && v > 0 && v < 1<<62 {
		e.Views = int64(v)
	}
	if ts, ok := jsonNumber(obj, "timestamp"); ok && ts > 0 && ts < 1<<40 {
		e.PublishedAt = time.Unix(int64(ts), 0).UTC()
	} else if d, err := time.Parse("20060102", jsonText(obj, "upload_date")); err == nil {
		e.PublishedAt = d.UTC()
	}
	return e, true
}

func jsonText(obj map[string]any, key string) string {
	s, _ := obj[key].(string)
	return strings.TrimSpace(s)
}

func jsonNumber(obj map[string]any, key string) (float64, bool) {
	n, ok := obj[key].(float64)
	return n, ok
}

// FormatDuration renders seconds as m:ss or h:mm:ss; it returns "" for
// non-positive input.
func FormatDuration(seconds int) string { return formatDuration(seconds) }
