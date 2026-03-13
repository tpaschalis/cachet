# cachet

Memoized `json.Unmarshal` for Go. Same signature, cached results.

```
go get github.com/tpaschalis/cachet
```

## Usage

```go
var v MyStruct
err := cachet.Unmarshal(data, &v) // parses JSON
err  = cachet.Unmarshal(data, &v) // copies from cache
```

Custom decoder with your own cache or JSON library:

```go
dec := cachet.New(
    cachet.WithCache(myLRUCache),             // default: *sync.Map
    cachet.WithUnmarshalFunc(sonic.Unmarshal), // default: encoding/json
)
```

`dec.Clear()` flushes the cache.

### Cache interface

Anything with `Load`, `Store`, and `Clear`. `*sync.Map` works out of the box.

```go
type Cache interface {
    Load(key any) (value any, ok bool)
    Store(key, value any)
    Clear()
}
```

## How it works

Cache key is `(string(jsonBytes), reflect.Type)`.

- **Miss**: unmarshal into the caller's value, store an independent copy.
  Value-only types (int, string, bool, flat structs) are snapshotted via
  `reflect.ValueOf(v.Interface())`; types with slices/maps/pointers are
  deep-copied so the cache never shares mutable state with the first caller.
- **Hit**: `reflect.Set` the stored value into the caller — no parsing.
  This is a shallow copy, so reference-typed fields (slices, maps, pointers)
  share backing data with the cache. Treat hit values as read-only, or copy
  before mutating.
- **Errors**: cached too. Repeated bad payloads return the stored error
  without re-parsing.

## Benchmarks

```
                     encoding/json     cachet hit     cachet miss
SmallPayload          716 ns/op        122 ns/op       2764 ns/op
LargePayload        92600 ns/op        352 ns/op     118304 ns/op
```

Hits: ~6x faster (small), ~260x faster (large).
Misses: ~4x slower (small), ~1.3x slower (large). The small-payload
miss is dominated by `sync.Map` and key allocation overhead relative
to a fast unmarshal; for large payloads the deep copy cost is dwarfed
by the parse itself.

## Trade-offs

The default `sync.Map` grows without bound. For many-unique-payload
workloads, plug in an LRU via `WithCache`.

## Alternatives

- **`sync.Once`** — if you only unmarshal once at startup, a `sync.Once` is simpler.
- **[goccy/go-json](https://github.com/goccy/go-json)** — faster decoder (~2x); use via `WithUnmarshalFunc` if you want both.
- **[kofalt/go-memoize](https://github.com/kofalt/go-memoize)** — generic function memoizer with TTL.
- **[shogo82148/memoize](https://github.com/shogo82148/memoize)** — `singleflight` + caching with generics.

## License

MIT. See [LICENSE](LICENSE).
