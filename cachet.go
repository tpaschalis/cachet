// Package cachet provides memoized JSON unmarshalling.
//
// It is a drop-in replacement for json.Unmarshal that caches results.
// When the same JSON bytes are unmarshalled to the same target type,
// the cached bytes are used instead of re-parsing from scratch.
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
// TODO: Consider adding a generic API to avoid unmarshal on cache hit entirely:
//
//	func Get[T any](d *Decoder, data []byte) (T, error)
//
// A generic version could store the unmarshalled Go value directly and return
// it by value on cache hit — a true zero-cost hit for plain structs. This would
// require Go 1.18+ and a different API shape, but could be offered alongside
// the traditional Unmarshal for users who want maximum performance.
package cachet

import (
	"encoding/json"
	"errors"
	"reflect"
	"sync"
)

// UnmarshalFunc is the function signature for JSON unmarshalling.
// encoding/json.Unmarshal and all popular alternatives (json-iterator,
// goccy/go-json, bytedance/sonic) share this signature.
type UnmarshalFunc func(data []byte, v any) error

// Cache is the interface for pluggable cache storage backends.
// *sync.Map satisfies this interface with no wrapper needed.
type Cache interface {
	Load(key any) (value any, ok bool)
	Store(key, value any)
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
func (d *Decoder) Unmarshal(data []byte, v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return errors.New("cachet: v must be a non-nil pointer")
	}

	// TODO: Allow users to provide a custom KeyFunc(data []byte, typ reflect.Type) any
	// to avoid the string(data) conversion. For example, users with large payloads
	// could supply an xxhash or SHA256-based key function that hashes the bytes
	// instead of copying them into a string, trading CPU for memory.
	key := cacheKey{
		data: string(data),
		typ:  rv.Type().Elem(),
	}

	// Cache hit: unmarshal from the stored bytes.
	if cached, ok := d.cache.Load(key); ok {
		return d.unmarshal(cached.([]byte), v)
	}

	// Cache miss: unmarshal the original data.
	if err := d.unmarshal(data, v); err != nil {
		return err
	}

	// Store a copy of the input bytes so the caller can't mutate them.
	stored := make([]byte, len(data))
	copy(stored, data)
	d.cache.Store(key, stored)

	return nil
}

// defaultDecoder is used by the package-level Unmarshal function.
var defaultDecoder = New()

// Unmarshal is a convenience wrapper that uses a default Decoder
// backed by a sync.Map and encoding/json.
func Unmarshal(data []byte, v any) error {
	return defaultDecoder.Unmarshal(data, v)
}
