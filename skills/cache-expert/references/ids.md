# IDs and Digests

IDs are the foundation of Dagger's caching system. An ID is a content-addressed
representation of an operation and all its inputs, forming a DAG.

Each Call has:
- a "recipe digest" (the call digest) that identifies the operation and its
  declared inputs, and
- an optional "content digest" that captures the actual content/result when
  that differs from the recipe.

These two digests are tracked separately.

## Structure

An ID is a base64-encoded protobuf message. See `dagql/call/callpbv1/call.proto`
for the full proto definitions.

**Key messages:**

- **DAG**: Contains `rootDigest` (the root Call's recipe digest) and
  `callsByDigest` (a map keyed by recipe digest).
- **Call**: Represents a single operation with `receiverDigest` (parent recipe
  digest), `field` (operation name), `args`, `type` (return type), `nth`,
  `module`, `view`, `digest` (recipe), `isCustomDigest`, and `contentDigest`
  (optional).
- **Literal**: Argument values. Can be primitives OR `callDigest` (a reference
  to another Call by recipe digest). This is how the DAG forms.

The `callsByDigest` map enables deduplication: if the same Call appears
multiple times in a DAG, it's stored once and referenced by digest.
`contentDigest` is stored on the Call but is not used for map keys or
references.

Argument order matters for digests. In dagql we keep args in a deterministic
order (often alphabetical) when building IDs.

**Sensitive arguments:**
- Arguments can be marked sensitive in the Go wrapper types.
- Sensitive args are excluded from `Call.Args` in the encoded DAG, excluded
  from digest calculation, and omitted from display output.
- Sensitive args cannot be recovered when decoding an ID.

## Digest Types

### Recipe digest (Call.Digest)

- The primary identity of a Call; used as the key in `callsByDigest` and in
  `Literal.callDigest` references.
- Normally computed from the call structure, see `calcDigest` in
  `dagql/call/id.go`.
- Can be overridden via `WithCustomDigest()`/`WithDigest()`.

### Content digest (Call.ContentDigest)

- Optional digest of the call's result content (e.g. filesystem contents).
- Stored on the Call but does **not** affect the Call's recipe digest.
- When hashing references to another ID, the recipe digest prefers the
  referenced ID's content digest when available.

## Wrapper Types

The Go code wraps proto types for immutability and convenience:

| Go Type | Proto Type | File |
|---------|------------|------|
| `call.ID` | `callpbv1.Call` | `dagql/call/id.go` |
| `call.Argument` | `callpbv1.Argument` | `dagql/call/argument.go` |
| `call.Literal` (interface) | `callpbv1.Literal` | `dagql/call/literal.go` |
| `call.Module` | `callpbv1.Module` | `dagql/call/module.go` |
| `call.Type` | `callpbv1.Type` | `dagql/call/type.go` |

Access the underlying proto via `.Call()`, `.PB()`, `.pb`, etc. but avoid
mutating it directly.

## Digest Computation (Recipe Digest)

A Call's recipe digest is a hash of its components (Merkle-DAG style):

- receiver digest: uses receiver content digest if present, otherwise receiver
  recipe digest
- type (including list/non-null structure)
- field (operation name)
- args (name + literal), skipping sensitive args
- nth (list selection index)
- module (callDigest/name/ref/pin)
- view

Literal hashing uses a type-prefix byte to avoid collisions:
- LiteralID uses the referenced ID's content digest if set, else its recipe
  digest.
- Lists and objects hash each element/argument in order with delimiters.
- Primitive literals hash their typed values.

See `calcDigest` and `appendLiteralBytes` in `dagql/call/id.go`.

### Custom Digests

A Call's recipe digest can be overridden via `WithCustomDigest()`/`WithDigest()`.
When `isCustomDigest = true`, the digest was explicitly set rather than
computed. The `ID` helpers preserve an existing custom digest across
`.With(...)` unless explicitly overridden.

### Content Digests

Set with `WithContentDigest()` (an IDOpt used with `id.With(...)`) and read with
`ContentDigest()`. Content digests are not included in the call's recipe digest,
but they do influence the recipe digests of callers that reference the ID.

## Important APIs

### Construction

| Method | Purpose |
|--------|---------|
| `call.New()` | Returns nil (the implicit Query root) |
| `id.Append(type, field, opts...)` | Primary way to build IDs. Creates a new ID with `id` as receiver, calling `field`. |
| `id.With(opts...)` | Creates new ID with options applied (WithArgs, WithModule, etc.) |
| `id.WithDigest(digest)` | Creates new ID with custom recipe digest |
| `WithContentDigest(digest)` | IDOpt: sets or clears content digest (use with `id.With(...)`) |
| `id.WithArgument(arg)` | Creates new ID with argument added/replaced |
| `id.SelectNth(n)` | Creates new ID selecting nth element from a list result |

### Accessors

| Method | Returns |
|--------|---------|
| `id.Receiver()` | Parent ID (nil for Query root) |
| `id.Field()` | Operation name |
| `id.Args()` | Arguments slice (may include sensitive args not in the proto) |
| `id.Arg(name)` | Single argument by name |
| `id.Type()` | Return type |
| `id.Digest()` | Recipe digest |
| `id.ContentDigest()` | Content digest (if set) |
| `id.Inputs()` | All referenced recipe digests (receiver + args) |

### Serialization

| Method | Purpose |
|--------|---------|
| `id.Encode()` | Serialize to base64 string (deterministic proto encoding) |
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
    "xxh3:abc...": Call{
      field: "withExec",
      receiverDigest: "xxh3:222...",
      args: [{name: "args", value: ["echo", "hi"]}],
      contentDigest: "xxh3:def..." // optional, if content-addressed
    }
  }
}
```

Each Call references its parent via `receiverDigest`, forming the chain. The
`contentDigest` is stored on the Call but is not part of the `callsByDigest`
key.

## Gotchas

- `Display()` is expensive: it walks the entire DAG without deduplication. For
  large DAGs this is very slow. Use `DisplaySelf()` if you only need the leaf
  operation.
- Sensitive args are **not encoded** or hashed. They are omitted from
  `Call.Args`, excluded from digests, and not shown in Display; decoding an ID
  cannot recover them.
- IDs are immutable. Methods like `Append`, `WithDigest`, `WithArgument`, and
  `WithContentDigest` return new IDs rather than mutating.
