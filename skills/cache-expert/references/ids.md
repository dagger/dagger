# IDs and Digests

IDs are the foundation of Dagger's caching system. An ID is a content-addressed representation of an operation and all its inputs, forming a DAG.

## Structure

An ID is a base64-encoded protobuf message. See `dagql/call/callpbv1/call.proto` for the full proto definitions.

**Key messages:**

- **DAG**: Contains `rootDigest` (the root Call's digest) and `callsByDigest` (a map of all Calls, deduplicated by digest)
- **Call**: Represents a single operation with `receiverDigest` (parent), `field` (operation name), `args`, `type` (return type), and `digest`
- **Literal**: Argument values. Can be primitives OR `callDigest` - a reference to another Call. This is how the DAG forms.

The `callsByDigest` map enables deduplication: if the same Call appears multiple times in a DAG, it's stored once and referenced by digest.

## Wrapper Types

The Go code wraps proto types for immutability and convenience:

| Go Type | Proto Type | File |
|---------|------------|------|
| `call.ID` | `callpbv1.Call` | `dagql/call/id.go` |
| `call.Argument` | `callpbv1.Argument` | `dagql/call/argument.go` |
| `call.Literal` (interface) | `callpbv1.Literal` | `dagql/call/literal.go` |
| `call.Module` | `callpbv1.Module` | `dagql/call/module.go` |
| `call.Type` | `callpbv1.Type` | `dagql/call/type.go` |

Access the underlying proto via `.Call()`, `.pb`, etc. but avoid mutating it directly.

## Digest Computation

A Call's digest is a hash of its components, making it a **Merkle DAG**:

- `receiverDigest` (parent's digest - recursive)
- `type` (return type)
- `field` (operation name)
- `args` (each argument name + value, recursive for nested IDs)
- `nth` (list selection index)
- `module` (module info if applicable)
- `view` (view context)

See `id.go:566` (`calcDigest`) for implementation.

### Custom Digests

A Call's digest can be overridden via `WithDigest()`. When `isCustomDigest = true`, the digest was explicitly set rather than computed. Used for cache key customization (per-client, per-session, content-addressed, etc.).

## Important APIs

### Construction

| Method | Purpose |
|--------|---------|
| `call.New()` | Returns nil (the implicit Query root) |
| `id.Append(type, field, opts...)` | **Primary way to build IDs.** Creates a new ID with `id` as receiver, calling `field`. |
| `id.With(opts...)` | Creates new ID with options applied (WithArgs, WithModule, etc.) |
| `id.WithDigest(digest)` | Creates new ID with custom digest |
| `id.WithArgument(arg)` | Creates new ID with argument added/replaced |
| `id.SelectNth(n)` | Creates new ID selecting nth element from a list result |

### Accessors

| Method | Returns |
|--------|---------|
| `id.Receiver()` | Parent ID (nil for Query root) |
| `id.Field()` | Operation name |
| `id.Args()` | Arguments slice |
| `id.Arg(name)` | Single argument by name |
| `id.Type()` | Return type |
| `id.Digest()` | The digest |
| `id.Inputs()` | All referenced digests (receiver + args) |

### Serialization

| Method | Purpose |
|--------|---------|
| `id.Encode()` | Serialize to base64 string |
| `id.Decode(str)` | Deserialize from base64 string |
| `id.ToProto()` | Convert to `*callpbv1.DAG` |
| `id.FromProto(dag)` | Load from `*callpbv1.DAG` |

### Display

| Method | Purpose |
|--------|---------|
| `id.Display()` | Full path with type (e.g., `container.from(address: "alpine"): Container!`) |
| `id.DisplaySelf()` | Just this call, no receiver chain |
| `id.Path()` | Dotted path (e.g., `container.from.withExec`) |
| `id.Name()` | `Parent.field` format |

## Example: Decoded ID

For `container.from("alpine").withExec(["echo", "hi"])`:

```
DAG {
  rootDigest: "xxh3:abc..."
  callsByDigest: {
    "xxh3:111...": Call{field: "container", receiverDigest: ""},
    "xxh3:222...": Call{field: "from", receiverDigest: "xxh3:111...", args: [{name: "address", value: "alpine"}]},
    "xxh3:abc...": Call{field: "withExec", receiverDigest: "xxh3:222...", args: [{name: "args", value: ["echo", "hi"]}]}
  }
}
```

Each Call references its parent via `receiverDigest`, forming the chain.

## Gotchas

- **`Display()` is expensive**: It walks the entire DAG without deduplication. For large DAGs this is very slow. Use `DisplaySelf()` if you only need the leaf operation.

- **Immutability**: `ID` instances are immutable. Methods like `Append`, `WithDigest`, `WithArgument` return new IDs rather than mutating.
