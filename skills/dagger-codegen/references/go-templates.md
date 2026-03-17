# Go Template System

> **Load when:** Editing Go templates, debugging generated output, or confused about which template produces what.

## Template Directory

```
cmd/codegen/generator/go/templates/src/
├── dagger.gen.go.tmpl              # Entry point
├── _dagger.gen.go/                 # Sub-templates
│   ├── module.go.tmpl              # Module: imports + calls ModuleMainSrc()
│   ├── defs.go.tmpl                # Type definitions, Client struct
│   └── client.go.tmpl              # Standalone: Connect(), Close()
├── _types/                         # Type-specific
│   ├── object.go.tmpl              # Object methods
│   ├── scalar.go.tmpl
│   ├── input.go.tmpl
│   └── enum.go.tmpl
├── dag/
│   └── dag.gen.go.tmpl             # Global dag.* helpers (non-module only)
└── internal/dagger/
    └── dagger.gen.go.tmpl          # Module types package
```

## Three Conditional Modes

Every template decision uses these booleans:

| Condition | True When | Effect |
|-----------|-----------|--------|
| `IsModuleCode` | `ModuleConfig != nil && ModuleName != ""` | Generates module runtime |
| `IsStandaloneClient` | `ClientConfig != nil` | Includes Connect/Close |
| `IsPartial` | First pass of two-pass | Skips main() generation |

## Output Files by Mode

### Module (`IsModuleCode = true`)

**Two output files:**

| File | Package | Contains |
|------|---------|----------|
| `dagger.gen.go` | main | `main()`, `invoke()` dispatch |
| `internal/dagger/dagger.gen.go` | dagger | Type definitions |

**Why two?** User code in `main` imports `internal/dagger`. Types live there to avoid namespace pollution.

### Standalone Client (`IsStandaloneClient = true`)

| File | Package | Contains |
|------|---------|----------|
| `dagger.gen.go` | dagger | Types + `Connect()` + `Close()` |
| `dag/dag.gen.go` | dag | Global `dag.*` helpers |

### Library (`IsModuleCode = false, IsStandaloneClient = false`)

Same as standalone client, minus `Connect()`/`Close()`.

## Two-Pass Generation (Modules)

Go modules need two passes because generated code depends on user types.

**Pass 0 (Partial):**
- Generate skeleton `dagger.gen.go`
- Create starter `main.go` if missing
- `IsPartial() = true` → no main() yet

**Pass 1 (Complete):**
- Load Go package, introspect user types
- Generate full `invoke()` dispatch
- `IsPartial() = false`

## Template Functions

Defined in `cmd/codegen/generator/go/templates/functions.go:54`:

| Function | Purpose |
|----------|---------|
| `IsModuleCode()` | Check if generating module |
| `IsStandaloneClient()` | Check if generating client |
| `IsPartial()` | Check if first pass |
| `FormatName(s)` | GraphQL → Go name |
| `FormatReturnType(f)` | Field → return type |
| `ModuleMainSrc()` | Generate main() + invoke() |
| `IsArgOptional(arg)` | Check if argument is optional (has default OR nullable type) |
| `HasOptionals(args)` | Check if any argument in list is optional |

## Common Mistakes

1. **Wrong file** - Module types are in `internal/dagger/dagger.gen.go`, not root
2. **Forgot IsPartial** - main() only generates on pass 1
3. **Import confusion** - Standalone clients alias SDK as `dagClient`
4. **Template caching** - Must rebuild `cmd/codegen` after changes
5. **Wrong optionality check** - Use `IsArgOptional $arg` not `$arg.TypeRef.IsOptional`. The latter only checks if the type is nullable, missing arguments with default values. Similarly, use `HasOptionals $field.Args` not `$field.Args.HasOptionals` for consistency with the helper function.
