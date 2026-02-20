# Generated Clients

> **Load when:** Working on `dagger client install`, implementing ClientGenerator, or adding client support to an SDK.

## What It Is

Generated clients let regular programs (not modules) use Dagger APIs with full dependency support.

**Command:** `dagger client install` (experimental, hidden)

**Supported:** Go, TypeScript only

## vs In-Module Bindings

| Aspect | In-Module | Generated Client |
|--------|-----------|------------------|
| Context | Inside module function | Regular program |
| Connection | Automatic | `Connect()` / `Close()` |
| Dependencies | Engine resolves | `serveModuleDependencies()` |
| Command | `dagger develop` | `dagger client install` |

## Interface

At `core/sdk.go:20`:

```go
type ClientGenerator interface {
    GenerateClient(ctx, deps, outputDir, dev) error
}
```

## Output (Go)

```
output/
├── dagger/
│   ├── dagger.gen.go    # Types + Connect() + Close()
│   └── dag/
│       └── dag.gen.go   # Global dag.* helpers
```

## Key Code

In `_dagger.gen.go/client.go.tmpl`:

```go
func Connect(ctx context.Context, opts ...ClientOpt) (*Client, error) {
    // Start engine connection
    // Call serveModuleDependencies()
}

func (c *Client) Close() error { ... }
```

## Usage

```go
client, _ := dagger.Connect(ctx)
defer client.Close()

out, _ := client.Container().
    From("alpine").
    WithExec([]string{"echo", "hello"}).
    Stdout(ctx)
```

## Limitations

- Only Go and TypeScript implement `ClientGenerator`
- Experimental/hidden command
- Dependency serving may have edge cases
