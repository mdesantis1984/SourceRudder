package cache

import (
	"context"
	"testing"
	"time"

	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

func TestSearchCacheKeyVersionAndBoundaries(t *testing.T) {
	req := &types.SearchRequest{Query: "query"}
	key := GenerateSearchCacheKey("query", []string{"web"}, req, 10)
	if key == GenerateCacheKey("query", []string{"web"}, "") {
		t.Fatal("new key collided with legacy key")
	}
	if GenerateSearchCacheKey("ab", []string{"c"}, req, 10) == GenerateSearchCacheKey("a", []string{"bc"}, req, 10) {
		t.Fatal("query/source boundaries collided")
	}
	if key == GenerateSearchCacheKey("query", []string{"news"}, req, 10) {
		t.Fatal("connector sources collided")
	}
	explicit := *req
	explicit.MaxResults = 10
	if key != GenerateSearchCacheKey("query", []string{"web"}, &explicit, 10) {
		t.Fatal("equivalent effective limits should share a key")
	}
}

func TestSetWithMaxTTLCapsWithoutExtending(t *testing.T) {
	ctx := context.Background()
	for _, seconds := range []int{10, 300} {
		svc := NewService(seconds)
		svc.SetWithMaxTTL(ctx, "key", []byte("payload"), []string{"web"}, 30*time.Second)
		entry, ok, err := svc.Get(ctx, "key")
		if err != nil || !ok {
			t.Fatalf("entry missing: %v", err)
		}
		want := time.Duration(seconds) * time.Second
		if want > 30*time.Second {
			want = 30 * time.Second
		}
		if got := entry.ExpiresAt.Sub(entry.CreatedAt); got != want {
			t.Fatalf("configured TTL=%ds: got %v want %v", seconds, got, want)
		}
		// Expire deterministically rather than sleeping for the cache lifetime.
		svc.mu.Lock()
		svc.entries["key"].expires = time.Now().Add(-time.Second)
		svc.mu.Unlock()
		if _, ok, _ := svc.Get(ctx, "key"); ok {
			t.Fatal("expired capped entry was returned")
		}
	}
}
