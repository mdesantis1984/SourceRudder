package cache

import (
	"context"
	"testing"
)

// TestCacheServiceGetExactKeyLookup proves that Get returns the
// stored entry for the exact key that was used at Set time, and
// returns (nil, false, nil) for any non-matching key. Triangulates
// the hit/miss path so a regression that hashes or transforms the
// key inside Get surfaces here.
func TestCacheServiceGetExactKeyLookup(t *testing.T) {
	s := NewService(60)
	ctx := context.Background()

	s.Set(ctx, "alpha", []byte("A"), []string{"web"})
	s.Set(ctx, "alpha-extra", []byte("B"), []string{"github"})

	got, ok, err := s.Get(ctx, "alpha")
	if err != nil {
		t.Fatalf("Get alpha: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true for stored key, got false")
	}
	if got == nil || string(got.Payload) != "A" {
		t.Fatalf("expected payload A, got %#v", got)
	}

	notStored, ok, err := s.Get(ctx, "missing")
	if err != nil {
		t.Fatalf("Get missing: %v", err)
	}
	if ok {
		t.Errorf("expected ok=false for missing key, got true")
	}
	if notStored != nil {
		t.Errorf("expected nil entry for missing key, got %#v", notStored)
	}

	// Substring must NOT match — proves the lookup is exact.
	if _, ok, err := s.Get(ctx, "alpha-ex"); err != nil {
		t.Fatalf("Get alpha-ex: %v", err)
	} else if ok {
		t.Errorf("alpha-ex substring must not match alpha-extra; got ok=true")
	}
}

// TestCacheServiceDeleteIfPresentPresent covers the present path:
// after Set, DeleteIfPresent MUST return true and the entry MUST
// disappear from Get.
func TestCacheServiceDeleteIfPresentPresent(t *testing.T) {
	s := NewService(60)
	ctx := context.Background()
	s.Set(ctx, "k", []byte("v"), nil)

	if removed := s.DeleteIfPresent(ctx, "k"); !removed {
		t.Fatal("expected DeleteIfPresent to return true for present key")
	}
	if _, ok, _ := s.Get(ctx, "k"); ok {
		t.Error("expected key to be gone after DeleteIfPresent")
	}
}

// TestCacheServiceDeleteIfPresentAbsent covers the absent path:
// DeleteIfPresent MUST return false (idempotent) and MUST NOT panic
// when the key is missing.
func TestCacheServiceDeleteIfPresentAbsent(t *testing.T) {
	s := NewService(60)
	ctx := context.Background()

	if removed := s.DeleteIfPresent(ctx, "missing"); removed {
		t.Fatal("expected DeleteIfPresent to return false for absent key")
	}

	// Calling it twice in a row stays idempotent.
	if removed := s.DeleteIfPresent(ctx, "missing"); removed {
		t.Fatal("expected DeleteIfPresent to remain false on second call")
	}
}

// TestCacheServiceDeleteIfPresentRaceSafe proves the check-and-delete
// pair is atomic under concurrent DeleteIfPresent calls. With a
// shared key, exactly one caller must observe removed=true.
func TestCacheServiceDeleteIfPresentRaceSafe(t *testing.T) {
	s := NewService(60)
	ctx := context.Background()
	s.Set(ctx, "k", []byte("v"), nil)

	const N = 64
	results := make(chan bool, N)
	for i := 0; i < N; i++ {
		go func() { results <- s.DeleteIfPresent(ctx, "k") }()
	}
	trues := 0
	for i := 0; i < N; i++ {
		if <-results {
			trues++
		}
	}
	if trues != 1 {
		t.Fatalf("exactly one goroutine must observe removed=true; got %d", trues)
	}
}
