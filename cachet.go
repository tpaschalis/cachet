// Package cachet memoizes json.Unmarshal. Same signature, cached results.
//
//	err := cachet.Unmarshal(data, &v) // first call parses JSON
//	err  = cachet.Unmarshal(data, &v) // second call copies from cache
//
// Both successful values and errors are cached. The cache key is
// (json bytes, target type). Hits are served via reflect.Set — no
// parsing. Errors are returned as-is on repeat calls.
//
// Hits are shallow copies. Types with slices, maps, or pointers
// share underlying data with the cache — treat as read-only.
package cachet

import (
	"encoding/json"
	"reflect"
	"sync"
)

// UnmarshalFunc matches the signature of json.Unmarshal (and sonic, go-json, etc.).
type UnmarshalFunc func(data []byte, v any) error

// Cache is the storage backend. *sync.Map satisfies it out of the box.
type Cache interface {
	Load(key any) (value any, ok bool)
	Store(key, value any)
	Clear()
}

// cacheKey is the map key: (raw JSON as string, target type).
type cacheKey struct {
	data string       // string(inputBytes)
	typ  reflect.Type // target type (not the pointer type)
}

// hasReferenceFields reports whether t contains a slice, map, pointer,
// interface, chan, or func (directly or in nested struct fields).
// When true, caching requires a second unmarshal to get an independent copy.
// Results are memoized per type.
var refFieldCache sync.Map // reflect.Type → bool

func hasReferenceFields(t reflect.Type) bool {
	if v, ok := refFieldCache.Load(t); ok {
		return v.(bool)
	}
	result := computeHasReferenceFields(t)
	refFieldCache.Store(t, result)
	return result
}

func computeHasReferenceFields(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Slice, reflect.Map, reflect.Pointer,
		reflect.Interface, reflect.Chan, reflect.Func:
		return true
	case reflect.Array:
		return hasReferenceFields(t.Elem())
	case reflect.Struct:
		for i := range t.NumField() {
			if hasReferenceFields(t.Field(i).Type) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// cacheEntry holds either a successful value or an error.
type cacheEntry struct {
	val any
	err error
}

// Decoder is a JSON unmarshaller with a result cache.
type Decoder struct {
	cache     Cache
	unmarshal UnmarshalFunc
}

// Option configures a Decoder.
type Option func(*Decoder)

// WithCache sets the cache backend (default: *sync.Map).
func WithCache(c Cache) Option {
	return func(d *Decoder) {
		d.cache = c
	}
}

// WithUnmarshalFunc sets the unmarshal function (default: encoding/json.Unmarshal).
func WithUnmarshalFunc(fn UnmarshalFunc) Option {
	return func(d *Decoder) {
		d.unmarshal = fn
	}
}

// New creates a Decoder. Zero options gives sync.Map + encoding/json.
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

// Unmarshal decodes JSON into v (must be a non-nil pointer).
// Cache hits copy the stored value via reflect.Set — no parsing.
// Both values and errors are cached.
func (d *Decoder) Unmarshal(data []byte, v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return &json.InvalidUnmarshalError{Type: reflect.TypeOf(v)}
	}

	key := cacheKey{
		data: string(data),
		typ:  rv.Type().Elem(),
	}

	// Cache hit: return the stored error or copy the stored value.
	if cached, ok := d.cache.Load(key); ok {
		entry := cached.(cacheEntry)
		if entry.err != nil {
			return entry.err
		}
		rv.Elem().Set(reflect.ValueOf(entry.val))
		return nil
	}

	// Cache miss: unmarshal into the caller's target.
	if err := d.unmarshal(data, v); err != nil {
		d.cache.Store(key, cacheEntry{err: err})
		return err
	}

	// Store an independent copy. Value-only types copy for free via
	// reflect.Set; reference types need a second unmarshal.
	if hasReferenceFields(key.typ) {
		cp := reflect.New(key.typ)
		if err := d.unmarshal(data, cp.Interface()); err != nil {
			return nil
		}
		d.cache.Store(key, cacheEntry{val: cp.Elem().Interface()})
	} else {
		d.cache.Store(key, cacheEntry{val: rv.Elem().Interface()})
	}

	return nil
}

// Clear flushes the cache.
func (d *Decoder) Clear() {
	d.cache.Clear()
}

var defaultDecoder = New()

// Unmarshal is a package-level convenience using the default Decoder.
func Unmarshal(data []byte, v any) error {
	return defaultDecoder.Unmarshal(data, v)
}
