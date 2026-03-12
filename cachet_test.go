package cachet

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
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

	// person has only value-typed fields, so the cache miss stores via
	// reflect.Set (1 unmarshal). The hit uses reflect.Set (0 unmarshals).
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected 1 unmarshal call (value-type fast path), got %d", got)
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

func (c *trackingCache) Clear() {
	c.inner.Clear()
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

// TestSliceFieldMutationSafe proves the second unmarshal (to create the
// cached copy) is necessary. A shallow reflect.Set would share the slice's
// backing array between the caller and the cache. Mutating an element in
// the caller's slice would then silently corrupt the cached value. The
// second unmarshal gives the cache its own independent allocations.
func TestSliceFieldMutationSafe(t *testing.T) {
	type tagged struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}

	dec := New()
	data := []byte(`{"name":"x","tags":["a","b","c"]}`)

	var v1 tagged
	if err := dec.Unmarshal(data, &v1); err != nil {
		t.Fatal(err)
	}

	// Mutate a slice element in-place (not append — this modifies the
	// backing array that a shallow copy would share with the cache).
	v1.Tags[0] = "CORRUPTED"

	// A second unmarshal should return the original tags from the cache.
	var v2 tagged
	if err := dec.Unmarshal(data, &v2); err != nil {
		t.Fatal(err)
	}
	if v2.Tags[0] != "a" || v2.Tags[1] != "b" || v2.Tags[2] != "c" {
		t.Fatalf("cache was corrupted by caller mutation: tags = %v", v2.Tags)
	}
}

func TestHasReferenceFields(t *testing.T) {
	tests := []struct {
		name string
		typ  reflect.Type
		want bool
	}{
		{"string", reflect.TypeOf(""), false},
		{"int", reflect.TypeOf(0), false},
		{"bool", reflect.TypeOf(true), false},
		{"flat struct", reflect.TypeOf(person{}), false},
		{"struct with slice", reflect.TypeOf(struct{ Tags []string }{}), true},
		{"struct with map", reflect.TypeOf(struct{ M map[string]int }{}), true},
		{"struct with pointer", reflect.TypeOf(struct{ P *int }{}), true},
		{"struct with interface", reflect.TypeOf(struct{ V any }{}), true},
		{"nested flat struct", reflect.TypeOf(struct{ P person }{}), false},
		{"nested ref struct", reflect.TypeOf(struct {
			Inner struct{ Tags []string }
		}{}), true},
		{"array of values", reflect.TypeOf([3]int{}), false},
		{"array of slices", reflect.TypeOf([2][]string{}), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasReferenceFields(tt.typ); got != tt.want {
				t.Errorf("hasReferenceFields(%v) = %v, want %v", tt.typ, got, tt.want)
			}
		})
	}
}

func TestValueTypeFastPath(t *testing.T) {
	// person is purely value-typed: miss should unmarshal once (no second copy).
	var calls atomic.Int64
	dec := New(WithUnmarshalFunc(func(data []byte, v any) error {
		calls.Add(1)
		return json.Unmarshal(data, v)
	}))

	data := []byte(`{"name":"fast","age":1}`)
	var p person
	_ = dec.Unmarshal(data, &p) // miss
	_ = dec.Unmarshal(data, &p) // hit

	if got := calls.Load(); got != 1 {
		t.Fatalf("value-type: expected 1 unmarshal call, got %d", got)
	}
}

func TestReferenceTypeSlowPath(t *testing.T) {
	// tagged has a []string field: miss should unmarshal twice (caller + cache copy).
	type tagged struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}

	var calls atomic.Int64
	dec := New(WithUnmarshalFunc(func(data []byte, v any) error {
		calls.Add(1)
		return json.Unmarshal(data, v)
	}))

	data := []byte(`{"name":"slow","tags":["a"]}`)
	var v tagged
	_ = dec.Unmarshal(data, &v) // miss: 2 unmarshals
	_ = dec.Unmarshal(data, &v) // hit: 0 unmarshals

	if got := calls.Load(); got != 2 {
		t.Fatalf("reference-type: expected 2 unmarshal calls, got %d", got)
	}
}

func TestNilPointerError(t *testing.T) {
	dec := New()
	err := dec.Unmarshal([]byte(`{}`), (*person)(nil))

	var target *json.InvalidUnmarshalError
	if !errors.As(err, &target) {
		t.Fatalf("expected *json.InvalidUnmarshalError, got %T: %v", err, err)
	}

	// Verify message matches stdlib.
	stdlibErr := json.Unmarshal([]byte(`{}`), (*person)(nil))
	if err.Error() != stdlibErr.Error() {
		t.Fatalf("error message mismatch:\n  cachet: %s\n  stdlib: %s", err, stdlibErr)
	}
}

func TestNonPointerError(t *testing.T) {
	dec := New()
	err := dec.Unmarshal([]byte(`{}`), person{})

	var target *json.InvalidUnmarshalError
	if !errors.As(err, &target) {
		t.Fatalf("expected *json.InvalidUnmarshalError, got %T: %v", err, err)
	}

	// Verify message matches stdlib.
	stdlibErr := json.Unmarshal([]byte(`{}`), person{})
	if err.Error() != stdlibErr.Error() {
		t.Fatalf("error message mismatch:\n  cachet: %s\n  stdlib: %s", err, stdlibErr)
	}
}

func TestClear(t *testing.T) {
	var calls atomic.Int64
	dec := New(WithUnmarshalFunc(func(data []byte, v any) error {
		calls.Add(1)
		return json.Unmarshal(data, v)
	}))

	data := []byte(`{"name":"clear","age":1}`)

	var p person
	_ = dec.Unmarshal(data, &p) // miss: 1 unmarshal (value-type fast path)

	dec.Clear()

	_ = dec.Unmarshal(data, &p) // miss again after clear: 1 more call
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected 2 unmarshal calls after clear, got %d", got)
	}
}

// Benchmarks: worst case (all misses) vs stdlib json.Unmarshal.
// cachet will always be slower on pure misses due to the string(data)
// conversion, reflect.TypeOf, and sync.Map overhead. These benchmarks
// quantify exactly how much.

// smallPayload is a typical small JSON object.
var smallPayload = []byte(`{"name":"alice","age":30}`)

// largePayload simulates a more realistic API response.
var largePayload = makeLargePayload()

func makeLargePayload() []byte {
	type item struct {
		ID     int    `json:"id"`
		Name   string `json:"name"`
		Email  string `json:"email"`
		Active bool   `json:"active"`
	}
	items := make([]item, 100)
	for i := range items {
		items[i] = item{ID: i, Name: "user", Email: "user@example.com", Active: true}
	}
	b, _ := json.Marshal(items)
	return b
}

func BenchmarkSmallPayload_StdlibOnly(b *testing.B) {
	for b.Loop() {
		var p person
		_ = json.Unmarshal(smallPayload, &p)
	}
}

func BenchmarkSmallPayload_CachetAllMisses(b *testing.B) {
	// Every iteration uses a unique payload to guarantee all misses.
	payloads := make([][]byte, b.N)
	for i := range payloads {
		payloads[i] = []byte(`{"name":"user` + strconv.Itoa(i) + `","age":` + strconv.Itoa(i) + `}`)
	}
	dec := New()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var p person
		_ = dec.Unmarshal(payloads[i], &p)
	}
}

func BenchmarkLargePayload_StdlibOnly(b *testing.B) {
	for b.Loop() {
		var r []struct {
			ID     int    `json:"id"`
			Name   string `json:"name"`
			Email  string `json:"email"`
			Active bool   `json:"active"`
		}
		_ = json.Unmarshal(largePayload, &r)
	}
}

func BenchmarkLargePayload_CachetAllMisses(b *testing.B) {
	// Generate unique but valid payloads by varying the first item's ID.
	// Must use b.N loop (not b.Loop()) because payloads are pre-generated.
	type item struct {
		ID     int    `json:"id"`
		Name   string `json:"name"`
		Email  string `json:"email"`
		Active bool   `json:"active"`
	}
	payloads := make([][]byte, b.N)
	for i := range payloads {
		items := make([]item, 100)
		for j := range items {
			items[j] = item{ID: i*100 + j, Name: "user", Email: "user@example.com", Active: true}
		}
		payloads[i], _ = json.Marshal(items)
	}
	dec := New()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var r []item
		_ = dec.Unmarshal(payloads[i], &r)
	}
}

func BenchmarkSmallPayload_CachetAllHits(b *testing.B) {
	dec := New()
	// Prime the cache.
	var p person
	_ = dec.Unmarshal(smallPayload, &p)
	b.ResetTimer()
	for b.Loop() {
		var p person
		_ = dec.Unmarshal(smallPayload, &p)
	}
}

func BenchmarkLargePayload_CachetAllHits(b *testing.B) {
	dec := New()
	// Prime the cache.
	var r []struct {
		ID     int    `json:"id"`
		Name   string `json:"name"`
		Email  string `json:"email"`
		Active bool   `json:"active"`
	}
	_ = dec.Unmarshal(largePayload, &r)
	b.ResetTimer()
	for b.Loop() {
		var r []struct {
			ID     int    `json:"id"`
			Name   string `json:"name"`
			Email  string `json:"email"`
			Active bool   `json:"active"`
		}
		_ = dec.Unmarshal(largePayload, &r)
	}
}
