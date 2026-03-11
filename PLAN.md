# Implementation Plan: cachet — Memoized JSON Unmarshalling

## Overview

A drop-in replacement for `json.Unmarshal` that memoizes results. When the same JSON bytes are unmarshalled to the same target type, the cached result is used instead of re-parsing from scratch.

## Architecture

### Core Types

```go
// UnmarshalFunc matches the signature of json.Unmarshal and all popular alternatives.
type UnmarshalFunc func(data []byte, v any) error

// MarshalFunc matches the signature of json.Marshal.
type MarshalFunc func(v any) ([]byte, error)

// Cache is the interface for pluggable cache backends.
// *sync.Map satisfies this out of the box.
type Cache interface {
    Load(key any) (value any, ok bool)
    Store(key, value any)
}

// Decoder is a memoized JSON decoder.
type Decoder struct {
    cache     Cache
    unmarshal UnmarshalFunc
    marshal   MarshalFunc
}
```

### Cache Key

```go
type cacheKey struct {
    data string       // string(inputBytes)
    typ  reflect.Type // reflect.TypeOf(v).Elem()
}
```

Struct key avoids delimiter collisions. `reflect.Type` is interned by the Go runtime so equality is cheap.

### Cache Hit Path (JSON round-trip)

- **Miss**: `unmarshal(data, v)` → `marshal(result)` → `cache.Store(key, marshalledBytes)`
- **Hit**: `cache.Load(key)` → `unmarshal(cachedBytes, v)`

Each caller gets a fresh unmarshal into their own `v`. No deep-copy bugs possible.

### Configuration via functional options

```go
func New(opts ...Option) *Decoder
func WithCache(c Cache) Option
func WithUnmarshalFunc(fn UnmarshalFunc) Option
func WithMarshalFunc(fn MarshalFunc) Option
```

### Package-level convenience

```go
var defaultDecoder = New()
func Unmarshal(data []byte, v any) error  // uses defaultDecoder
```

## Implementation Steps

### Step 1: Initialize Go module
- `go mod init github.com/tpaschalis/cachet`

### Step 2: Create `cachet.go`
- `Cache` interface
- `UnmarshalFunc` / `MarshalFunc` types
- `cacheKey` struct (unexported)
- `Decoder` struct
- `Option` type + `WithCache`, `WithUnmarshalFunc`, `WithMarshalFunc`
- `New()` constructor (defaults: `&sync.Map{}`, `json.Unmarshal`, `json.Marshal`)
- `(*Decoder).Unmarshal()` method with the core algorithm
- `defaultDecoder` + package-level `Unmarshal()`
- TODO comment about future generics-based `Get[T]()` API

### Step 3: Create `cachet_test.go`
- Cache miss then hit returns same result
- Mutation of returned value doesn't corrupt cache
- Different target types with same JSON get separate entries
- Concurrent access safety
- Custom cache backend verification
- Custom unmarshal/marshal functions
- Error cases: nil pointer, invalid JSON not cached

### Step 4: Run tests, verify, commit, push
