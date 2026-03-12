// Package cachet provides memoized JSON unmarshalling.
//
// It wraps any json.Unmarshal-compatible function and caches the
// unmarshalled Go values. On a cache hit the stored value is copied
// into the caller's target via reflect.Set — no JSON parsing happens.
//
// Basic usage with the package-level function:
//
//	var v MyStruct
//	err := cachet.Unmarshal(data, &v)
//
// Custom configuration:
//
//	dec := cachet.New(
//	    cachet.WithCache(myLRUCache),
//	    cachet.WithUnmarshalFunc(sonic.Unmarshal),
//	)
//	err := dec.Unmarshal(data, &v)
//
// Cache hits return a shallow copy. If the target type contains slices,
// maps, or pointer fields, treat the returned value as read-only or
// copy those fields before mutating.
package cachet

import (
	"encoding/json"
	"reflect"
	"sync"
)

// UnmarshalFunc is the function signature for JSON unmarshalling.
// encoding/json.Unmarshal and all popular alternatives (json-iterator,
// goccy/go-json, bytedance/sonic) share this signature.
type UnmarshalFunc func(data []byte, v any) error

// Cache is the interface for pluggable cache storage backends.
// *sync.Map satisfies this interface out of the box.
type Cache interface {
	Load(key any) (value any, ok bool)
	Store(key, value any)
	Clear()
}

// cacheKey is used as the map key. Both fields are comparable,
// so this struct works as a sync.Map key.
type cacheKey struct {
	data string       // string(inputBytes)
	typ  reflect.Type // target type (not the pointer type)
}

// Decoder is a memoized JSON decoder.
type Decoder struct {
	cache     Cache
	unmarshal UnmarshalFunc
}

// Option configures a Decoder.
type Option func(*Decoder)

// WithCache sets the cache backend. Default is *sync.Map.
func WithCache(c Cache) Option {
	return func(d *Decoder) {
		d.cache = c
	}
}

// WithUnmarshalFunc sets the JSON unmarshal function.
// Default is encoding/json.Unmarshal.
func WithUnmarshalFunc(fn UnmarshalFunc) Option {
	return func(d *Decoder) {
		d.unmarshal = fn
	}
}

// New creates a Decoder with the given options.
// With no options, it uses a sync.Map cache and encoding/json.Unmarshal.
func New(opts ...Option) *Decoder {
	d := &Decoder{
		cache:     &sync.Map{},
		unmarshal: json.Unmarshal,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Unmarshal decodes JSON data into v, using the cache when possible.
// v must be a non-nil pointer, as with encoding/json.Unmarshal.
//
// On a cache hit the stored Go value is copied into v via reflect.Set.
// No JSON parsing occurs. The copy is shallow: if the target type
// contains slices, maps, or pointer fields, the caller shares the
// underlying data with the cache. Treat such fields as read-only or
// copy them before mutating.
func (d *Decoder) Unmarshal(data []byte, v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return &json.InvalidUnmarshalError{Type: reflect.TypeOf(v)}
	}

	key := cacheKey{
		data: string(data),
		typ:  rv.Type().Elem(),
	}

	// Cache hit: copy the stored value into the caller's target.
	if cached, ok := d.cache.Load(key); ok {
		rv.Elem().Set(reflect.ValueOf(cached))
		return nil
	}

	// Cache miss: unmarshal into the caller's target.
	if err := d.unmarshal(data, v); err != nil {
		return err
	}

	// Store an independent copy so caller mutations don't corrupt
	// the cache. We unmarshal a second time into a fresh value.
	cp := reflect.New(key.typ)
	if err := d.unmarshal(data, cp.Interface()); err != nil {
		// The first unmarshal succeeded, so this shouldn't fail.
		// If it does, we skip caching but still return the result.
		return nil
	}
	d.cache.Store(key, cp.Elem().Interface())

	return nil
}

// Clear removes all entries from the cache.
func (d *Decoder) Clear() {
	d.cache.Clear()
}

// defaultDecoder is used by the package-level Unmarshal function.
var defaultDecoder = New()

// Unmarshal is a convenience wrapper that uses a default Decoder
// backed by a sync.Map and encoding/json.
func Unmarshal(data []byte, v any) error {
	return defaultDecoder.Unmarshal(data, v)
}
