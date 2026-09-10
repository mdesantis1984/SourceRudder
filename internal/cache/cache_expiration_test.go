package cache

import (
	"context"
	"testing"
	"time"
)

func TestCacheServiceKeysPurgesExpiredEntries(t *testing.T) {
	service := NewService(0)
	service.Set(context.Background(), "expired", []byte("value"), nil)
	time.Sleep(time.Millisecond)

	keys, err := service.Keys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Fatalf("expired entries remain visible: %v", keys)
	}
	if len(service.entries) != 0 {
		t.Fatalf("expired entries remain retained: %d", len(service.entries))
	}
}

func TestCacheServiceSetPurgesExpiredEntriesBeforeInsert(t *testing.T) {
	service := NewService(0)
	service.Set(context.Background(), "expired", []byte("old"), nil)
	time.Sleep(time.Millisecond)
	service.ttl = time.Minute

	service.Set(context.Background(), "live", []byte("new"), nil)

	if len(service.entries) != 1 {
		t.Fatalf("expected only the live entry, got %d entries", len(service.entries))
	}
	if _, ok := service.entries["live"]; !ok {
		t.Fatal("live entry was not retained")
	}
}

func TestCacheServiceExpiredGetSweepsExpiredEntriesAndKeepsLiveOnes(t *testing.T) {
	service := NewService(60)
	now := time.Now()
	service.entries["requested-expired"] = &entry{key: "requested-expired", expires: now.Add(-time.Minute)}
	service.entries["other-expired"] = &entry{key: "other-expired", expires: now.Add(-time.Second)}
	live := &entry{key: "live", value: []byte("fresh"), expires: now.Add(time.Minute)}
	service.entries["live"] = live

	if _, ok, err := service.Get(context.Background(), "requested-expired"); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("expired entry was returned as a hit")
	}
	if len(service.entries) != 1 || service.entries["live"] != live {
		t.Fatalf("expiration sweep did not preserve only the live entry: %#v", service.entries)
	}
}

func TestExpirationSweepKeepsFreshSameKeyReplacement(t *testing.T) {
	service := NewService(60)
	now := time.Now()
	service.entries["shared"] = &entry{key: "shared", expires: now.Add(-time.Minute)}
	observedExpired := service.entries["shared"]
	fresh := &entry{key: "shared", value: []byte("fresh"), expires: now.Add(time.Minute)}

	// Model a writer replacing the value after Get observed expiry but before its sweep.
	service.entries["shared"] = fresh
	service.mu.Lock()
	service.deleteExpiredLocked(now)
	service.mu.Unlock()

	if service.entries["shared"] != fresh || service.entries["shared"] == observedExpired {
		t.Fatal("expiration sweep removed the fresh same-key replacement")
	}
}
