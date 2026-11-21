package resultset

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

func extItems(prefix string, from, to int) []types.SearchResultItem {
	out := make([]types.SearchResultItem, 0, to-from)
	for i := from; i < to; i++ {
		out = append(out, types.SearchResultItem{Title: "t", URL: fmt.Sprintf("https://example.test/%s/%d", prefix, i)})
	}
	return out
}

func growBy(n int, calls *int32) ExtendFunc {
	return func(c Continuation, have []types.SearchResultItem) ([]types.SearchResultItem, Continuation, error) {
		atomic.AddInt32(calls, 1)
		c.NextPage++
		return extItems("x", len(have), len(have)+n), c, nil
	}
}

func TestExtendAppendsOncePerPositionUnderConcurrency(t *testing.T) {
	st := New(Config{})
	id, _ := st.Put(Set{Results: extItems("a", 0, 5), Continuation: &Continuation{NextPage: 2}})
	var calls int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			set, ok, err := st.Extend(id, 5, growBy(3, &calls))
			if !ok || err != nil || len(set.Results) != 8 {
				t.Errorf("ok=%v err=%v len=%d", ok, err, len(set.Results))
			}
		}()
	}
	wg.Wait()
	if calls != 1 {
		t.Fatalf("fetch ran %d times, want 1", calls)
	}
	if got := st.Stats().Results; got != 8 {
		t.Fatalf("stats results = %d, want 8", got)
	}
}

func TestExtendKeepsSetOnErrorAndSkipsWhenNotPastEnd(t *testing.T) {
	st := New(Config{})
	id, _ := st.Put(Set{Results: extItems("a", 0, 5), Continuation: &Continuation{NextPage: 2}})
	boom := errors.New("boom")
	set, ok, err := st.Extend(id, 5, func(Continuation, []types.SearchResultItem) ([]types.SearchResultItem, Continuation, error) {
		return nil, Continuation{}, boom
	})
	if !ok || !errors.Is(err, boom) || len(set.Results) != 5 || set.Continuation == nil || set.Continuation.NextPage != 2 {
		t.Fatalf("error must leave the set unchanged: ok=%v err=%v set=%+v", ok, err, set)
	}
	var calls int32
	if _, _, err := st.Extend(id, 2, growBy(1, &calls)); err != nil || calls != 0 {
		t.Fatalf("offset inside the stored results must not fetch: calls=%d err=%v", calls, err)
	}
	if _, ok, _ := st.Extend("rs_missing", 5, growBy(1, &calls)); ok || calls != 0 {
		t.Fatalf("unknown ID must not fetch")
	}
}

func TestExtendStopsAtResultBudget(t *testing.T) {
	st := New(Config{MaxResults: 8})
	id, _ := st.Put(Set{Results: extItems("a", 0, 5), Continuation: &Continuation{NextPage: 2}})
	var calls int32
	set, ok, err := st.Extend(id, 5, growBy(10, &calls))
	if !ok || err != nil || len(set.Results) != 8 {
		t.Fatalf("budget must trim to 8: ok=%v err=%v len=%d", ok, err, len(set.Results))
	}
	if !set.Continuation.Done || !set.Continuation.BudgetHit {
		t.Fatalf("budget stop must close the continuation: %+v", set.Continuation)
	}
	if again, _, _ := st.Extend(id, 8, growBy(1, &calls)); len(again.Results) != 8 || calls != 1 {
		t.Fatalf("closed continuation must not fetch again: len=%d calls=%d", len(again.Results), calls)
	}
}
