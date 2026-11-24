package connectors

import (
	"context"
	"os"
	"testing"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/internal/youtube"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

// TestYouTubeDirectSearchLive runs the real yt-dlp against YouTube. It needs
// the network, so it only runs with SOURCERUDDER_LIVE_YTDLP=1
// (YTDLP_PATH overrides the binary).
func TestYouTubeDirectSearchLive(t *testing.T) {
	if os.Getenv("SOURCERUDDER_LIVE_YTDLP") != "1" {
		t.Skip("set SOURCERUDDER_LIVE_YTDLP=1 to run the live yt-dlp check")
	}
	binary := os.Getenv("YTDLP_PATH")
	if binary == "" {
		binary = "yt-dlp"
	}
	c := NewYouTubeConnector("http://127.0.0.1:1", cache.NewService(60)) // SearXNG must not be needed
	c.SetDirectSearch(youtube.NewService(youtube.Config{Binary: binary}, nil))
	resp, err := c.Search(context.Background(), &types.SearchRequest{Query: "golang context tutorial", MaxResults: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Warnings) != 0 || resp.Partial {
		t.Fatalf("live search fell back: partial=%v warnings=%q", resp.Partial, resp.Warnings)
	}
	if len(resp.Results) == 0 || len(resp.Results) > 5 {
		t.Fatalf("results = %d, want 1..5", len(resp.Results))
	}
	for i, r := range resp.Results {
		t.Logf("%d. %s | %s | %s | %s", i+1, r.Title, r.URL, r.Author, r.Snippet)
		if r.Engine != "yt-dlp" || r.Title == "" || r.Author == "" || r.Snippet == "" {
			t.Errorf("result %d is incomplete: %+v", i+1, r)
		}
	}
}
