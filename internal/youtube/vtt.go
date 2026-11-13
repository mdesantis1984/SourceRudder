package youtube

import (
	"html"
	"regexp"
	"strings"
)

var (
	tagPattern        = regexp.MustCompile(`<[^>]*>`)
	whitespacePattern = regexp.MustCompile(`\s+`)
)

// VTTToText converts WebVTT subtitles to plain text: it drops the header,
// NOTE/STYLE/REGION blocks, cue identifiers and timings, strips <...> tags
// and HTML entities, and removes the repeated lines that YouTube automatic
// captions carry from one cue to the next.
func VTTToText(vtt string) string {
	vtt = strings.ReplaceAll(strings.ReplaceAll(vtt, "\r\n", "\n"), "\r", "\n")
	blocks := strings.Split(vtt, "\n\n")
	var out []string
	recent := func(line string) bool {
		for i := len(out) - 1; i >= 0 && i >= len(out)-2; i-- {
			if out[i] == line {
				return true
			}
		}
		return false
	}
	for _, block := range blocks {
		lines := strings.Split(strings.Trim(block, "\n"), "\n")
		if len(lines) == 0 {
			continue
		}
		first := strings.TrimSpace(lines[0])
		if strings.HasPrefix(first, "WEBVTT") || strings.HasPrefix(first, "NOTE") ||
			strings.HasPrefix(first, "STYLE") || strings.HasPrefix(first, "REGION") {
			continue
		}
		inCue := false
		for _, line := range lines {
			if strings.Contains(line, "-->") {
				inCue = true
				continue
			}
			if !inCue {
				// Cue identifier or settings before the timing line.
				continue
			}
			text := tagPattern.ReplaceAllString(line, "")
			text = html.UnescapeString(text)
			text = strings.ReplaceAll(text, "\u00a0", " ")
			text = strings.TrimSpace(whitespacePattern.ReplaceAllString(text, " "))
			if text == "" || recent(text) {
				continue
			}
			out = append(out, text)
		}
	}
	return strings.Join(out, "\n")
}
