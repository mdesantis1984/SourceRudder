// Package youtube serves YouTube video transcripts through yt-dlp instead of
// scraping watch pages, which YouTube answers with HTTP 429 or a redirect to
// google.com/sorry once an IP is flagged.
//
// Every uncached request makes exactly two yt-dlp invocations: one for the
// video metadata (info JSON) and one for a single subtitle track chosen from
// that metadata. Downloading several tracks of auto-dubbed videos was enough
// to trigger 429s, so only one track is ever requested. A detected block is
// reported once, never retried, never cached, and starts a short global
// cooldown so the server stops hitting YouTube while the IP is flagged.
package youtube
