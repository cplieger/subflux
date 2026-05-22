package authhandlers

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestShardedCeremonyMap_Store_Load_roundtrip(t *testing.T) {
	t.Parallel()
	sm := NewShardedCeremonyMap[PendingTOTP]()
	val := PendingTOTP{CreatedAt: time.Now(), UserID: 42, IP: "1.2.3.4"}
	if !sm.Store("key1", val) {
		t.Fatal("Store returned false")
	}
	got, ok := sm.LoadAndDelete("key1")
	if !ok {
		t.Fatal("LoadAndDelete returned false")
	}
	if got.UserID != 42 {
		t.Errorf("UserID = %d, want 42", got.UserID)
	}
}

func TestShardedCeremonyMap_Delete_removes(t *testing.T) {
	t.Parallel()
	sm := NewShardedCeremonyMap[string]()
	sm.Store("k", "v")
	sm.LoadAndDelete("k")
	_, ok := sm.LoadAndDelete("k")
	if ok {
		t.Error("LoadAndDelete after delete should return false")
	}
}

func TestShardedCeremonyMap_max_capacity(t *testing.T) {
	t.Parallel()
	sm := NewShardedCeremonyMap[int]()
	// Fill to capacity.
	for i := range MaxCeremonySessions {
		if !sm.Store(fmt.Sprintf("k%d", i), i) {
			t.Fatalf("Store failed at %d, expected success up to %d", i, MaxCeremonySessions)
		}
	}
	// Next store should fail.
	if sm.Store("overflow", 999) {
		t.Error("Store should return false when at capacity")
	}
}

func TestShardedCeremonyMap_concurrent(t *testing.T) {
	t.Parallel()
	sm := NewShardedCeremonyMap[int]()
	var wg sync.WaitGroup
	for i := range 200 {
		wg.Go(func() {
			key := fmt.Sprintf("k%d", i%50)
			if i%3 == 0 {
				sm.Store(key, i)
			} else {
				sm.LoadAndDelete(key)
			}
		})
	}
	wg.Wait()
}
