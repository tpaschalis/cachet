# cachet

Memoized JSON unmarshalling for Go. Drop-in replacement for `json.Unmarshal`
that caches the unmarshalled Go value so repeated calls with the same payload
skip JSON parsing entirely.

## Install

```
go get github.com/tpaschalis/cachet
```

## Usage

Use it exactly like `json.Unmarshal`:

```go
var v MyStruct
err := cachet.Unmarshal(data, &v)
```

Or configure your own decoder with a custom cache backend or JSON library:

```go
dec := cachet.New(
    cachet.WithCache(myLRUCache),             // default: *sync.Map
    cachet.WithUnmarshalFunc(sonic.Unmarshal), // default: encoding/json
)
err := dec.Unmarshal(data, &v)
```

Call `dec.Clear()` to flush the cache when you need to.

### Cache interface

Any type that implements `Load`, `Store`, and `Clear` works as a backend.
`*sync.Map` satisfies this out of the box.

```go
type Cache interface {
    Load(key any) (value any, ok bool)
    Store(key, value any)
    Clear()
}
```

## How it works

The cache key is `(string(jsonBytes), reflect.Type)`. On a miss, the data
is unmarshalled into the caller's value and a second unmarshal produces an
independent copy for the cache. On a hit, the stored Go value is copied
into the caller's target via `reflect.Set` -- no JSON parsing happens.

Cache hits return a shallow copy. For structs with only value-type fields
(int, string, bool, etc.) this is safe. If your type contains slices, maps,
or pointer fields, treat the returned value as read-only or copy those
fields before mutating.

## Benchmarks

10 runs each, `encoding/json` as the baseline.

```
goos: linux
goarch: amd64

                     encoding/json     cachet hit     cachet miss
SmallPayload          620 ns/op        127 ns/op       2247 ns/op
LargePayload        82915 ns/op       2047 ns/op     109813 ns/op
```

Cache hits are ~5x faster on small payloads and ~40x faster on large
payloads. The miss overhead comes from the second unmarshal (to create
the cached copy), `string(data)` key conversion, and `sync.Map` bookkeeping.

## Trade-offs

The default `sync.Map` cache grows without bound. For workloads with
many unique payloads, use `WithCache` to plug in an LRU or size-bounded
backend and call `Clear` as needed.

## License

MIT. See [LICENSE](LICENSE).
