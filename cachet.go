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
	"unsafe"
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

// unsafeString returns a string that shares b's backing array.
// The result is only valid while b is alive and unmodified.
//
// NOTE: the returned string MUST only be used for cache lookups
// (sync.Map.Load) — it must never be stored. The default *sync.Map
// does not retain the lookup key. Custom Cache implementations
// MUST NOT retain the key passed to Load; doing so creates a
// dangling pointer once the caller's data slice is reused or collected.
func unsafeString(b []byte) string {
	return unsafe.String(unsafe.SliceData(b), len(b))
}

// hasReferenceFields reports whether t contains a slice, map, pointer,
// interface, chan, or func in its exported, JSON-reachable fields.
// When true, caching requires a deep copy to get an independent value.
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
		// Only check exported fields: encoding/json never writes to
		// unexported fields, so they can't cause shared-state issues.
		// We also skip fields with `json:"-"` tags.
		for i := range t.NumField() {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			if tag := f.Tag.Get("json"); tag == "-" {
				continue
			}
			if hasReferenceFields(f.Type) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// hasLiveRefs reports whether v contains any non-nil pointer, non-nil
// interface, non-empty slice, or non-empty map. When false, a shallow
// copy (reflect.Set) of v is already an independent value — no deep
// copy is needed even if the type has reference-typed fields.
func hasLiveRefs(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		return !v.IsNil()
	case reflect.Slice, reflect.Map:
		return v.Len() > 0
	case reflect.Struct:
		for i := range v.NumField() {
			if hasLiveRefs(v.Field(i)) {
				return true
			}
		}
	}
	return false
}

// deepCopyValue returns an independent deep copy of v. Slices, maps,
// pointers, and interfaces are recursively copied so the result shares
// no mutable state with the original. Scalars and strings are returned
// as-is (they are already copied by value).
func deepCopyValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		cp := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := range v.Len() {
			cp.Index(i).Set(deepCopyValue(v.Index(i)))
		}
		return cp
	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		cp := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			cp.SetMapIndex(deepCopyValue(iter.Key()), deepCopyValue(iter.Value()))
		}
		return cp
	case reflect.Pointer:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		cp := reflect.New(v.Type().Elem())
		cp.Elem().Set(deepCopyValue(v.Elem()))
		return cp
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		// Dynamic value may be map[string]any, []any, etc.
		inner := deepCopyValue(v.Elem())
		return inner
	case reflect.Struct:
		cp := reflect.New(v.Type()).Elem()
		for i := range v.NumField() {
			cp.Field(i).Set(deepCopyValue(v.Field(i)))
		}
		return cp
	default:
		// Scalars, strings: already copied by value.
		return v
	}
}

// cacheEntry holds either a successful value or an error.
type cacheEntry struct {
	val reflect.Value
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

	// Build a lookup key using a zero-copy transient string.
	// This avoids a []byte→string allocation on cache hits.
	key := cacheKey{
		data: unsafeString(data),
		typ:  rv.Type().Elem(),
	}

	// Cache hit: return the stored error or copy the stored value.
	if cached, ok := d.cache.Load(key); ok {
		entry := cached.(cacheEntry)
		if entry.err != nil {
			return entry.err
		}
		rv.Elem().Set(entry.val)
		return nil
	}

	// Cache miss: allocate an owned string copy for storage,
	// then unmarshal into the caller's target.
	key.data = string(data)

	if err := d.unmarshal(data, v); err != nil {
		d.cache.Store(key, cacheEntry{err: err})
		return err
	}

	// Store an independent copy for the cache.
	// We must snapshot via Interface() to detach from the caller's variable;
	// a bare rv.Elem() is an addressable reflect.Value that aliases the
	// caller's memory and would be mutated if they change their variable.
	if hasReferenceFields(key.typ) {
		if hasLiveRefs(rv.Elem()) {
			// Reference fields are populated: deep-copy to avoid sharing
			// backing arrays/maps/pointers with the caller's value.
			cached := deepCopyValue(rv.Elem())
			d.cache.Store(key, cacheEntry{val: cached})
		} else {
			// All reference fields are nil/empty: shallow copy is safe.
			d.cache.Store(key, cacheEntry{val: reflect.ValueOf(rv.Elem().Interface())})
		}
	} else {
		d.cache.Store(key, cacheEntry{val: reflect.ValueOf(rv.Elem().Interface())})
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
