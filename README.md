# cachet

Memoized JSON unmarshalling for Go. Drop-in replacement for `json.Unmarshal`
that caches results so repeated calls with the same payload skip redundant work.

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

### Cache interface

Any type that implements `Load` and `Store` works as a cache backend.
`*sync.Map` satisfies this out of the box.

```go
type Cache interface {
    Load(key any) (value any, ok bool)
    Store(key, value any)
}
```

## How it works

The cache key is the raw JSON bytes (as a string) combined with the target
Go type. On a cache miss, the data is unmarshalled normally and a copy of
the input bytes is stored. On a cache hit, the stored bytes are unmarshalled
into the caller's target.

Each caller gets a fresh unmarshal into their own value. There are no
shared pointers or deep-copy concerns.

## Benchmarks

Worst case: every call is a cache miss (unique payloads, no hits).
This measures the pure overhead cachet adds on top of `encoding/json`.

```
goos: linux
goarch: amd64
cpu: Intel(R) Xeon(R) Processor @ 2.80GHz

Benchmark              stdlib (ns/op)  cachet miss (ns/op)    delta
SmallPayload (~25B)           775 ±14            2247 ±99   +190.0%
LargePayload (~6KB)        98687 ±1933        109813 ±3768  +11.3%
```

The overhead comes from `string(data)` key conversion, `reflect.TypeOf`,
and `sync.Map` bookkeeping. It shrinks proportionally with payload size
because the actual unmarshal dominates. On cache hits the cost is one map
lookup plus an unmarshal from an in-memory byte slice.

## Trade-offs

This is a v1 that optimizes for correctness and simplicity. Two areas
are explicitly left for future work:

- **Custom key function** -- allow users to provide their own hash
  (e.g. xxhash) instead of `string(data)`, trading CPU for memory on
  large payloads.
- **Generic API** -- a `Get[T any]()` that stores the unmarshalled Go
  value directly and returns it by copy, eliminating the unmarshal on
  cache hits entirely.

## License

MIT. See [LICENSE](LICENSE).
