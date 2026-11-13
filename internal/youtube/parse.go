package youtube

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	videoIDPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	languagePattern = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8}){0,3}$`)
)

// ParseVideoID returns the video ID of a YouTube video URL (watch, youtu.be,
// shorts, embed, live, on www/m/music/youtube-nocookie hosts, with or
// without scheme) or of a bare 11-character ID. Channels, searches,
// playlists and other URLs are not videos.
func ParseVideoID(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if videoIDPattern.MatchString(raw) {
		return raw, true
	}
	return parseVideoURL(raw)
}

// IsVideoURL reports whether raw is a YouTube video URL. Unlike
// ParseVideoID it does not accept a bare ID, so non-URL input keeps the
// generic fetch behavior.
func IsVideoURL(raw string) bool {
	_, ok := parseVideoURL(strings.TrimSpace(raw))
	return ok
}

// CanonicalURL returns the watch URL for a video ID.
func CanonicalURL(id string) string {
	return "https://www.youtube.com/watch?v=" + id
}

func parseVideoURL(raw string) (string, bool) {
	if raw == "" {
		return "", false
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	var id string
	switch host {
	case "youtu.be", "www.youtu.be":
		id = segments[0]
	case "youtube.com", "www.youtube.com", "m.youtube.com", "music.youtube.com",
		"youtube-nocookie.com", "www.youtube-nocookie.com":
		switch {
		case len(segments) == 1 && segments[0] == "watch":
			id = u.Query().Get("v")
		case len(segments) == 2 && (segments[0] == "shorts" || segments[0] == "embed" || segments[0] == "live" || segments[0] == "v"):
			id = segments[1]
		}
	}
	if !videoIDPattern.MatchString(id) {
		return "", false
	}
	return id, true
}
