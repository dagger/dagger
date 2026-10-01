---
name: adding-pragmas
description: Add or change a Dagger module pragma/directive/decorator — the `@check`, `@generate`, `@up`, `@agent`, `@defaultPath`, `@ignorePatterns` family that authors write as `// +check` (Go), `@check()` (TypeScript), `@check` (Python), or `@check` (Dang). Use when adding a new marker, wiring an existing one into another SDK, or debugging why a marker set in module source doesn't reach the engine.
---

# Adding a module pragma

A pragma is one idea spelled four ways (a Go comment, a Python decorator, a
TypeScript decorator, a Dang directive) that all converge on a single GraphQL
directive on the module's rendered schema. Adding one means threading it
through the engine once, then through each SDK's own registration path.

Work engine-first: nothing in an SDK compiles until `withX` exists on the
generated client.

## 1. Engine

### 1a. Declare the directive

`core/schema/module.go`, the `moduleDirectives` slice (~line 44).

```go
{
    Name:        "agent",
    Description: dagql.FormatDescription(`Indicates that this function is an agent middleware, composed by dagger agent.`),
    Args:        dagql.NewInputSpecs(), // or InputSpec{Name: "path", Type: dagql.String("")}
    Locations: []dagql.DirectiveLocation{
        dagql.DirectiveLocationFieldDefinition,    // function-level
        // dagql.DirectiveLocationArgumentDefinition, // argument-level
    },
    ViewFilter: AfterVersion("v1.0.0-0"), // optional: gate to the v1 API surface
},
```

**Not `dagql/server.go`.** That file's `coreDirectives` (~line 365) is for
schema-wide built-ins (`@deprecated`, `@experimental`, `@sourceMap`), and its
copies of `check` / `generate` / `defaultPath` / `ignorePatterns` /
`defaultAddress` are stale leftovers. Both slices install into the same
name-keyed map and `moduleSchema.Install` runs last, so `moduleDirectives`
silently wins. Older guides pointed at `coreDirectives`; that is wrong for
anything module-facing.

### 1b. Carry it on the core type

`core/typedef.go`:

- Function-level → a field on `Function` (e.g. `IsAgent bool`, ~line 48).
- Argument-level → a field on `FunctionArg`, tagged `` `field:"true" doc:"..."` ``,
  plus a parameter on `Function.WithArg` and on `moduleSchema.functionWithArg`.

Then **the JSON mirrors near the bottom of the same file** (~lines 2708, 2885,
2934). These are the structs modules are serialized to and from. Skipping them
gives the classic symptom: the pragma works on a fresh module load and vanishes
after a cache round-trip.

### 1c. Emit the directive

`Function.Directives()` (~line 170) or `FunctionArg.Directives()`:

```go
if fn.IsAgent {
    directives = append(directives, &ast.Directive{Name: "agent"})
}
```

For list-valued args build an `ast.ChildValueList` and use `ast.ListValue`.

This is what actually puts `@agent` in the schema text — and what reads it back
out downstream, e.g. the LLM tool filter at `core/llm_object_tools.go:394`.

### 1d. Builder + API resolver

```go
// core/typedef.go
func (fn *Function) WithAgent() *Function { fn = fn.Clone(); fn.IsAgent = true; return fn }

// core/schema/module.go, inside moduleSchema.Install
dagql.Func("withAgent", s.functionWithAgent).
    View(AfterVersion("v1.0.0-0")).  // must match the directive's ViewFilter
    Doc(`Returns the function with a flag indicating it is an agent middleware.`),

func (s *moduleSchema) functionWithAgent(ctx context.Context, fn *core.Function, args struct{}) (*core.Function, error) {
    return fn.WithAgent(), nil
}
```

### 1e. Validation (optional)

`Module.validateObjectFunction` (`core/module.go:1312`) is where per-pragma
signature rules go — e.g. `@agent` rejects any required argument other than its
single `LLM!` base.

### 1f. Regenerate

`dagger generate` — every SDK's generated client needs `withAgent` /
`with_agent` before the SDK-side work will compile.

## 2. Go SDK — pragma comments

`cmd/codegen/generator/go/templates/module_funcs.go`, three edits:

```go
// ~line 76, alongside the check/generate/up blocks
if v, ok := docPragmas["agent"]; ok {
    if v == nil {
        spec.isAgent = true
    } else {
        spec.isAgent, ok = v.(bool)
        if !ok {
            return nil, fmt.Errorf("agent pragma %q, must be a valid boolean", v)
        }
    }
}

// ~line 169: field on the spec struct
isAgent bool

// ~line 235: emit the builder call
if spec.isAgent {
    fnTypeDefCode = dotLine(fnTypeDefCode, "WithAgent").Call()
}
```

Argument-level pragmas parse in `parseParamSpecVar` and land on `paramSpec`,
then flow into `argOpts` before `WithArg`. Non-boolean values decode with
`mapstructure.Decode` (lists) or a direct type assertion (strings).

## 3. Python SDK — standalone marker decorator

Modern markers are **separate decorators stacked under `@function`**, not
keyword arguments to it:

```python
@object_type
class MyModule:
    @function
    @agent
    def agent(self, base: dagger.LLM) -> dagger.LLM: ...
```

Seven edits, all under `sdk/python/src/dagger/mod/`:

| File | Edit |
|---|---|
| `_module.py` (~line 56) | `AGENT_DEF_KEY: typing.Final[str] = "__dagger_agent__"` |
| `_module.py` | `Module.agent()` decorator that `setattr(fn, AGENT_DEF_KEY, True)` |
| `_module.py` `function()` wrapper (~line 795) | read the attribute, pass into `FunctionDefinition` |
| `_module.py` `_typedefs()` (~line 211) | `if func.agent: func_def = func_def.with_agent()` |
| `_types.py` | `FunctionDefinition.agent: bool = False` |
| `_resolver.py` | the same `AGENT_DEF_KEY` const + an `agent` property |
| `mod/__init__.py` | `agent = _default_mod.agent` and add `"agent"` to `__all__` |

Two things that look redundant but aren't:

- **The key constant is declared twice**, in `_module.py` and `_resolver.py`.
  They don't import from each other; keep both in sync.
- **The resolver property ORs both sources**, so decorator order doesn't matter:

  ```python
  @property
  def agent(self) -> bool:
      return self.meta.agent or getattr(self.wrapped, AGENT_DEF_KEY, False)
  ```

Top-level `dagger/__init__.py` re-exports with `from dagger.mod import *`, so
`__all__` is the only export list to touch.

Gotcha: `@agent def agent(...)` works — decorator expressions resolve against
module globals before the class-local name is bound — but only for the *first*
such method in a class body. A second one would resolve to the method.

## 4. TypeScript SDK — no-op decorator + AST introspection + Go emitter

TypeScript decorators do nothing at runtime; they're read out of the AST at
introspection time. Ten edits — the most of any SDK, and the last five are the
ones older guides missed entirely:

| File | Edit |
|---|---|
| `src/module/registry.ts` | the no-op decorator method on `Registry` |
| `src/module/decorators.ts` | `export const agent = registry.agent` |
| `src/module/introspector/dagger_module/decorator.ts` | import it, add `"agent"` to the `DaggerDecorators` union, `export const AGENT_DECORATOR = agent.name as DaggerDecorators` |
| `src/module/introspector/dagger_module/function.ts` | `public isAgent = false` + `if (this.ast.isNodeDecoratedWith(this.node, AGENT_DECORATOR))` |
| `src/module/introspector/typedef_json.ts` `serializeFunction()` | `isAgent: f.isAgent === true` |
| `src/module/entrypoint/register.ts` `addFunction()` | `fnDef = fnDef.withAgent()` |
| `cmd/codegen/generator/typescript/templates/entrypoint_typedef.go` | `IsAgent bool \`json:"isAgent"\`` on `TypedefFunction` |
| `cmd/codegen/generator/typescript/templates/entrypoint_functions.go` | `if fn.IsAgent { parts = append(parts, ".withAgent()") }` |
| `sdk/typescript/runtime/tsutils/module/index.ts` | add the name to the explicit re-export list |
| `sdk/typescript/runtime/tsutils/module/core.d.ts` | `export function agent(): MethodDecorator` |

**The last two are the easiest to miss and fail the loudest.** A module's
`@dagger.io/dagger` does not resolve to `sdk/typescript/src/`; it resolves to a
generated `sdk/index.ts` embedded in the runtime, which re-exports a **hand-
maintained, explicit list** of names from the rolled-up bundle. Leave a name out
and everything compiles, then every module using it dies at load with:

```text
SyntaxError: The requested module '@dagger.io/dagger' does not provide an export named 'agent'
```

`core.d.ts` is the matching type stub — omit it and the decorator works at
runtime but the module fails to typecheck.

There are **two** registration paths and both need the flag: `register.ts`
(runtime) and the static-dispatch entrypoint that Go generates from
`typedef_json.ts` output. The Go structs are the deserialization contract for
that JSON — a missing field there means the flag is silently dropped.

`isNodeDecoratedWith` matches with `getText().startsWith(name)`, so avoid
decorator names that prefix another.

Note `DaggerFunction.toJSON()` deliberately omits these flags, so the
`scan.spec.ts` golden will *not* catch a missing one. `typedef_json.ts` is the
path that matters.

## 5. Dang

Dang consumes the directive directly. Add a case to
`functionDirectiveSelectors` in `core/sdk/dang/v2/helpers.go` (and `v1/` if the
pragma should exist there):

```go
case "agent":
    sels = append(sels, dagql.Selector{Field: "withAgent"})
```

Argument-level directives are handled by the sibling
`argumentDirectiveSelectors`.

## 6. Tests

**TypeScript introspector golden** — add a method to
`sdk/typescript/src/module/introspector/test/testdata/decorators/index.ts` and
the matching entry in `expected.json`.

**Integration** — per-SDK modules under `core/integration/testdata/<feature>/`,
driven by a table test. Minimum viable modules:

```text
editor-py/   dagger.json  pyproject.toml  src/editor_py/{__init__,main}.py
editor-ts/   dagger.json  package.json  tsconfig.json  src/index.ts
```

Copy `dagger.json` / `pyproject.toml` / `tsconfig.json` from a sibling under
`core/integration/testdata/`. No lockfile is needed. **`engineVersion` must be
at or above the directive's `ViewFilter`** — a `v0.20.1` module cannot see a
directive gated to `v1.0.0-0`.

**Running** (see also the `dagger-dev-tests` skill):

```bash
# integration — rebuilds the engine, so run this one first
./hack/dev dagger call --progress=dots engine-dev test --run 'TestAgents' --pkg ./core/integration/

# then reuse the deployed engine for the rest
./hack/with-dev dagger check --progress=dots typescript-client:test-nodejs-lts
./hack/with-dev dagger check --progress=dots typescript-client:lint-typescript typescript-client:format
./hack/with-dev dagger check --progress=dots python-client:lint python-client:python-313:unit
```

`hack/dev` rebuilds *and redeploys* the engine, so two concurrent invocations
fight over the `dagger-engine.dev` container and one dies with `removal of
container ... is already in progress`. Use `hack/dev` once, then `hack/with-dev`
(env only, no redeploy) for everything after.

Any change under `sdk/typescript/runtime/tsutils/` is `//go:embed`-ed into the
engine, so it needs a full `hack/dev` rebuild — `hack/with-dev` will keep
running the stale bundle.

The repo CLI on `$PATH` may be an older release that can't read this repo's
`dagger.toml` — symptom is `dagger check -l` printing an empty table. Always go
through `hack/dev` / `hack/with-dev`.

## Checklist

- [ ] `moduleDirectives` entry (+ `ViewFilter` if gating)
- [ ] core type field **and its JSON mirrors**
- [ ] `Directives()` emission
- [ ] `WithX()` + `dagql.Func("withX", ...)` with a matching `.View(...)`
- [ ] validation rule, if the pragma constrains signatures
- [ ] `dagger generate`
- [ ] Go codegen: parse, spec field, emit
- [ ] Python: 2 key consts, decorator, `FunctionDefinition`, resolver property, `_typedefs()`, `__all__`
- [ ] TypeScript: registry, export, constant, introspector, `typedef_json.ts`, `register.ts`, 2 Go emitter files, **`tsutils/module/index.ts` + `core.d.ts`**
- [ ] Dang helper, if applicable
- [ ] introspector golden + per-SDK integration testdata

## Order of operations

The cheapest path, learned the hard way — each step catches a class of mistake
the next one can't:

1. Engine + `dagger generate`, then `go build ./core/... ./cmd/...`.
2. Python. Its runtime imports the real `sdk/python/src`, so unit tests catch
   wiring errors fast.
3. TypeScript. Compiling and linting proves nothing about whether a module can
   *import* the decorator — only an integration test that loads a real
   TypeScript module exercises the embedded bundle's export list.
4. Full suite.

## Reference changes

- `@agent` in Python and TypeScript — the change this skill was written from.
- `abd1b062bd` — `@check`, the first of this family (also added the three
  `.contributing/*.md` guides this skill supersedes).
- `d8e77997d` (`@defaultPath`), `31a84b7ff` (`@ignorePatterns`) — argument-level
  pragmas with arguments.
