package resultset

import (
	"fmt"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newClock() *fakeClock { return &fakeClock{now: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)} }

func items(n int) []types.SearchResultItem {
	out := make([]types.SearchResultItem, n)
	for i := range out {
		out[i] = types.SearchResultItem{Title: fmt.Sprintf("t%d", i), URL: fmt.Sprintf("https://example.test/%d", i)}
	}
	return out
}

func TestResultSetStoreDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.TTL != 15*time.Minute || cfg.MaxSets != 64 || cfg.MaxResults != 2000 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestResultSetStoreIDFormat(t *testing.T) {
	store := New(DefaultConfig())
	re := regexp.MustCompile(`^rs_[A-Z2-7]{16}$`)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		id, _ := store.Put(Set{Query: "q", Results: items(1)})
		if !re.MatchString(id) {
			t.Fatalf("id %q does not match rs_ + 16 base32 chars", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

func TestResultSetStoreRoundTripCopiesData(t *testing.T) {
	store := New(DefaultConfig())
	in := items(3)
	id, stored := store.Put(Set{Query: "golang", DefaultLimit: 10, Results: in})
	if stored != 3 {
		t.Fatalf("stored=%d want 3", stored)
	}
	in[0].Title = "mutated by caller"
	got, ok := store.Get(id)
	if !ok || got.Query != "golang" || got.DefaultLimit != 10 || len(got.Results) != 3 {
		t.Fatalf("unexpected set: ok=%v %+v", ok, got)
	}
	if got.Results[0].Title != "t0" {
		t.Fatalf("store must copy results on Put; got %q", got.Results[0].Title)
	}
	got.Results[1].Title = "mutated by reader"
	again, _ := store.Get(id)
	if again.Results[1].Title != "t1" {
		t.Fatalf("store must copy results on Get; got %q", again.Results[1].Title)
	}
}

func TestResultSetStoreUnknownIDLeavesStoreUnchanged(t *testing.T) {
	store := New(DefaultConfig())
	store.Put(Set{Query: "q", Results: items(2)})
	before := store.Stats()
	if _, ok := store.Get("rs_AAAAAAAAAAAAAAAA"); ok {
		t.Fatal("unknown id must not be found")
	}
	if _, ok := store.Get(""); ok {
		t.Fatal("empty id must not be found")
	}
	if after := store.Stats(); after != before {
		t.Fatalf("Get on unknown id changed the store: before=%+v after=%+v", before, after)
	}
}

func TestResultSetStoreTTLExpiry(t *testing.T) {
	clock := newClock()
	store := New(Config{TTL: 15 * time.Minute, MaxSets: 8, MaxResults: 100, Now: clock.Now})
	id, _ := store.Put(Set{Query: "q", Results: items(2)})

	clock.Advance(15*time.Minute - time.Second)
	if _, ok := store.Get(id); !ok {
		t.Fatal("set must still be readable just before the TTL")
	}
	clock.Advance(2 * time.Second)
	if _, ok := store.Get(id); ok {
		t.Fatal("set must be expired after the TTL")
	}

	// A later Put purges the expired set and frees its result budget.
	store.Put(Set{Query: "q2", Results: items(1)})
	if st := store.Stats(); st.Sets != 1 || st.Results != 1 {
		t.Fatalf("expired set must be purged on Put: %+v", st)
	}
}

func TestResultSetStoreMaxSetsEvictsOldestFirst(t *testing.T) {
	clock := newClock()
	store := New(Config{TTL: time.Hour, MaxSets: 3, MaxResults: 1000, Now: clock.Now})
	var ids []string
	for i := 0; i < 5; i++ {
		clock.Advance(time.Second)
		id, _ := store.Put(Set{Query: fmt.Sprintf("q%d", i), Results: items(1)})
		ids = append(ids, id)
	}
	if st := store.Stats(); st.Sets != 3 {
		t.Fatalf("sets=%d want 3", st.Sets)
	}
	for i, id := range ids {
		_, ok := store.Get(id)
		if want := i >= 2; ok != want {
			t.Fatalf("set %d present=%v want %v", i, ok, want)
		}
	}
}

func TestResultSetStoreMaxResultsEvictsOldestFirst(t *testing.T) {
	clock := newClock()
	store := New(Config{TTL: time.Hour, MaxSets: 10, MaxResults: 10, Now: clock.Now})
	a, _ := store.Put(Set{Query: "a", Results: items(4)})
	clock.Advance(time.Second)
	b, _ := store.Put(Set{Query: "b", Results: items(4)})
	clock.Advance(time.Second)
	c, _ := store.Put(Set{Query: "c", Results: items(4)}) // 12 > 10: evicts a
	if _, ok := store.Get(a); ok {
		t.Fatal("oldest set must be evicted when the result budget is exceeded")
	}
	if _, ok := store.Get(b); !ok {
		t.Fatal("second oldest set must survive")
	}
	if _, ok := store.Get(c); !ok {
		t.Fatal("newest set must survive")
	}
	if st := store.Stats(); st.Sets != 2 || st.Results != 8 {
		t.Fatalf("stats=%+v want 2 sets / 8 results", st)
	}
}

func TestResultSetStoreOversizedSetIsTruncatedToBudget(t *testing.T) {
	store := New(Config{TTL: time.Hour, MaxSets: 10, MaxResults: 5})
	id, stored := store.Put(Set{Query: "big", Results: items(9)})
	if stored != 5 {
		t.Fatalf("stored=%d want 5", stored)
	}
	got, ok := store.Get(id)
	if !ok || len(got.Results) != 5 || got.Results[4].Title != "t4" {
		t.Fatalf("oversized set must keep its first 5 results: ok=%v len=%d", ok, len(got.Results))
	}
	if st := store.Stats(); st.Results > 5 {
		t.Fatalf("result budget exceeded: %+v", st)
	}
}

func TestResultSetStoreInvalidConfigFallsBackToDefaults(t *testing.T) {
	store := New(Config{})
	cfg := store.Config()
	def := DefaultConfig()
	if cfg.TTL != def.TTL || cfg.MaxSets != def.MaxSets || cfg.MaxResults != def.MaxResults {
		t.Fatalf("zero config must use defaults, got %+v", cfg)
	}
}

func TestResultSetStoreConcurrentAccess(t *testing.T) {
	store := New(Config{TTL: time.Minute, MaxSets: 8, MaxResults: 40})
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				id, _ := store.Put(Set{Query: fmt.Sprintf("q%d-%d", g, i), Results: items(3)})
				store.Get(id)
				store.Get("rs_AAAAAAAAAAAAAAAA")
				store.Stats()
			}
		}(g)
	}
	wg.Wait()
	st := store.Stats()
	if st.Sets > 8 || st.Results > 40 {
		t.Fatalf("bounds exceeded under concurrency: %+v", st)
	}
}
