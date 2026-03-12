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

- **Miss**: unmarshal into caller's value, store an independent copy.
  Value-only types (int, string, bool, flat structs) copy for free;
  types with slices/maps/pointers need a second unmarshal.
- **Hit**: `reflect.Set` the stored value into the caller — no parsing.
- **Errors**: cached too. Repeated bad payloads return the stored error
  without re-parsing.

Hits are shallow copies. Types with reference fields share underlying
data with the cache — treat as read-only or copy before mutating.

## Benchmarks

```
                     encoding/json     cachet hit     cachet miss
SmallPayload          620 ns/op        127 ns/op       2247 ns/op
LargePayload        82915 ns/op       2047 ns/op     109813 ns/op
```

Hits: ~5x faster (small), ~40x faster (large). Miss overhead is the
second unmarshal + `string(data)` conversion + `sync.Map` bookkeeping.

## Trade-offs

The default `sync.Map` grows without bound. For many-unique-payload
workloads, plug in an LRU via `WithCache`.

## Alternatives to consider

Depending on your workload, cachet may be overkill or the wrong tool:

- **`sync.Once` / lazy init** — if you unmarshal a payload once at startup
  and reuse the value, you don't need a cache library. A `sync.Once` or
  package-level `var` is simpler and has zero overhead.

- **[goccy/go-json](https://github.com/goccy/go-json)** — ~2x faster than
  `encoding/json` for typical structs, no caching layer needed. If your
  bottleneck is parsing speed rather than repeated payloads, a faster
  decoder gives you most of the win with none of the complexity.
  cachet can also wrap it via `WithUnmarshalFunc(gojson.Unmarshal)` for
  both benefits.

- **[kofalt/go-memoize](https://github.com/kofalt/go-memoize)** — generic
  function memoizer with TTL and purge. If you want to cache the result
  of *any* expensive function (not just JSON), this is more general.
  cachet is narrower on purpose: it knows about `reflect.Type` keys and
  shallow-copy semantics so you don't have to.

- **[shogo82148/memoize](https://github.com/shogo82148/memoize)** —
  `singleflight` + caching with generics. Good if you also need
  deduplication of in-flight calls (e.g. many goroutines requesting
  the same key concurrently).

## License

MIT. See [LICENSE](LICENSE).
