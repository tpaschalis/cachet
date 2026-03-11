package cachet

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
)

type person struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

type animal struct {
	Name    string `json:"name"`
	Species string `json:"species"`
}

func TestBasicCacheHit(t *testing.T) {
	var calls atomic.Int64
	dec := New(WithUnmarshalFunc(func(data []byte, v any) error {
		calls.Add(1)
		return json.Unmarshal(data, v)
	}))

	data := []byte(`{"name":"alice","age":30}`)

	var p1 person
	if err := dec.Unmarshal(data, &p1); err != nil {
		t.Fatal(err)
	}
	if p1.Name != "alice" || p1.Age != 30 {
		t.Fatalf("unexpected result: %+v", p1)
	}

	var p2 person
	if err := dec.Unmarshal(data, &p2); err != nil {
		t.Fatal(err)
	}
	if p2.Name != "alice" || p2.Age != 30 {
		t.Fatalf("unexpected result on cache hit: %+v", p2)
	}

	// First call = cache miss, second = cache hit. Both call unmarshal.
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected 2 unmarshal calls, got %d", got)
	}
}

func TestMutationDoesNotCorruptCache(t *testing.T) {
	dec := New()
	data := []byte(`{"name":"bob","age":25}`)

	var p1 person
	if err := dec.Unmarshal(data, &p1); err != nil {
		t.Fatal(err)
	}

	// Mutate the returned value.
	p1.Name = "MUTATED"
	p1.Age = 999

	// Second call should return the original values.
	var p2 person
	if err := dec.Unmarshal(data, &p2); err != nil {
		t.Fatal(err)
	}
	if p2.Name != "bob" || p2.Age != 25 {
		t.Fatalf("cache was corrupted: got %+v", p2)
	}
}

func TestDifferentTypesGetSeparateEntries(t *testing.T) {
	dec := New()
	data := []byte(`{"name":"charlie"}`)

	var p person
	if err := dec.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}

	var a animal
	if err := dec.Unmarshal(data, &a); err != nil {
		t.Fatal(err)
	}

	if p.Name != "charlie" {
		t.Fatalf("person: got %+v", p)
	}
	if a.Name != "charlie" {
		t.Fatalf("animal: got %+v", a)
	}
}

func TestConcurrentAccess(t *testing.T) {
	dec := New()
	data := []byte(`{"name":"concurrent","age":1}`)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var p person
			if err := dec.Unmarshal(data, &p); err != nil {
				t.Errorf("concurrent unmarshal: %v", err)
				return
			}
			if p.Name != "concurrent" || p.Age != 1 {
				t.Errorf("unexpected: %+v", p)
			}
		}()
	}
	wg.Wait()
}

func TestCustomCacheBackend(t *testing.T) {
	var loads, stores atomic.Int64
	cache := &trackingCache{loads: &loads, stores: &stores}
	dec := New(WithCache(cache))

	data := []byte(`{"name":"custom","age":1}`)

	var p person
	_ = dec.Unmarshal(data, &p)
	_ = dec.Unmarshal(data, &p)

	if got := loads.Load(); got != 2 {
		t.Fatalf("expected 2 cache loads, got %d", got)
	}
	if got := stores.Load(); got != 1 {
		t.Fatalf("expected 1 cache store, got %d", got)
	}
}

type trackingCache struct {
	inner  sync.Map
	loads  *atomic.Int64
	stores *atomic.Int64
}

func (c *trackingCache) Load(key any) (any, bool) {
	c.loads.Add(1)
	return c.inner.Load(key)
}

func (c *trackingCache) Store(key, value any) {
	c.stores.Add(1)
	c.inner.Store(key, value)
}

func TestInvalidJSON(t *testing.T) {
	dec := New()
	data := []byte(`{invalid}`)

	var p person
	err := dec.Unmarshal(data, &p)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}

	// Should not be cached — second call should also fail.
	var p2 person
	err = dec.Unmarshal(data, &p2)
	if err == nil {
		t.Fatal("expected error on second call with invalid JSON")
	}
}

func TestNilPointer(t *testing.T) {
	dec := New()
	err := dec.Unmarshal([]byte(`{}`), (*person)(nil))
	if err == nil {
		t.Fatal("expected error for nil pointer")
	}
}

func TestPackageLevelUnmarshal(t *testing.T) {
	data := []byte(`{"name":"global","age":42}`)
	var p person
	if err := Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	if p.Name != "global" || p.Age != 42 {
		t.Fatalf("unexpected: %+v", p)
	}
}

func TestInputSliceMutationSafe(t *testing.T) {
	dec := New()
	data := []byte(`{"name":"safe","age":1}`)

	var p1 person
	if err := dec.Unmarshal(data, &p1); err != nil {
		t.Fatal(err)
	}

	// Mutate the original input slice.
	for i := range data {
		data[i] = 'x'
	}

	// Cache hit should still work with the stored copy.
	original := []byte(`{"name":"safe","age":1}`)
	var p2 person
	if err := dec.Unmarshal(original, &p2); err != nil {
		t.Fatal(err)
	}
	if p2.Name != "safe" || p2.Age != 1 {
		t.Fatalf("cached bytes were corrupted: %+v", p2)
	}
}
