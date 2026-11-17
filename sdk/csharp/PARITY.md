# C# SDK — engine parity state

Status as of 2026-07-04 (branch `csharp/experimental`, rebased onto dagger main
`c7d72b6ce`): **all csharp integration coverage passing on the dev engine** —
the full `TestCsharp` suite, the csharp entries of
`TestInterface/TestIfaceBasic|TestIfaceCall`, all three checks matrices
(`TestChecksDirectSDK|ViaLegacyBlueprintConfig|AsToolchain`), and
`TestModule/TestContextGit`. Examples revalidated (9/9 module examples +
standalone-client).

## How to validate

```sh
# full sweep (from repo root; use PowerShell on Windows — the CLI hangs under Git Bash)
dagger call engine-dev test \
  --run 'TestCsharp|TestInterface/(TestIfaceBasic|TestIfaceCall)/.*csharp.*' \
  --pkg ./core/integration --test-verbose

# fast local codegen loop (no engine)
dotnet run --project codegen -- src/introspection.json src/Dagger.SDK/Dagger.SDK.g.cs
dotnet build src/Dagger.slnx -c Release

# refresh the checked-in introspection from the dev engine
dagger call engine-dev introspection-json export --path=src/introspection.json
```

## Engine-contract notes (current main)

- Object ids are the generic `ID` scalar; object-typed args carry an
  `@expectedType(name: "...")` directive. Typed `FooID` scalars and
  `loadFooFromID` fields exist only in the legacy view (engineVersion < 0.22).
- Objects are reloaded via `node(id:) { ... on Type }`. Inline fragments
  produce no nesting in response JSON (QueryExecutor skips those segments).
- Module functions and fields are served flat on the Query root; constructor
  args surface as `with(...)` / top-level `dagger call` flags.
- A module object **must register its constructor even when parameterless** —
  otherwise the engine synthesizes it with empty state and the module is never
  invoked (property initializers would not serialize).
- Object/interface values in module state round-trip as **id strings**.
- Public properties are auto-exposed as fields (Go/Java parity); methods
  require `[Function]`. Both `class` and `record` (record class) may carry
  `[Object]`: a record's public primary constructor maps to constructor args
  and its public init properties auto-expose as fields (the compiler-generated
  copy constructor is `protected`, so it does not trip the single-public-ctor
  rule). `record struct`/value types are intentionally unsupported — objects
  round-trip by reference/id, not by value. `[Function(Name=...)]` renames members; `[Name]` is
  parameter-only; `[Ignore]` is directory-pattern-only (parameter);
  `[Deprecated]` targets methods/properties/parameters.
- Checks return `void`/`Task` and fail by throwing (await `Sync()` to run a
  container), like every SDK: the engine rejects any other check return type
  (`check X.y must return Void`), and DAGGER017 reports it at compile time.
- Checks = `[Function] [Check]`; run by `dagger check` (workspace command —
  inference only discovers legacy `dagger.json`, not `dagger-module.toml`);
  verdicts render on progress output (`--progress=report`; a failing check
  shows `ERROR` only when its spans live in the CLI's trace — see below).
- Telemetry: invocation spans must parent from the engine's TRACEPARENT and
  the GraphQL client must send the live span id on REQUEST headers with
  .NET's automatic propagator disabled — otherwise engine-side work parents
  under a never-exported ghost span in a foreign trace and the frontend
  renders failing checks as canceled/skipped instead of ERROR.
- Interface conversions: `As{Interface}()` on objects is a local re-wrap (no
  GraphQL call, mirrors Go's AsNode); `AsInterface<T>()` converts dependency
  objects to locally-declared interfaces via the dynamic proxy (engine-checked
  at call time).

## Test-suite conventions (core/integration/module_csharp_test.go)

- `csharpModInit(t, c, name, mainCs)` — module at `/work/modules/<name>`,
  local `sdk/csharp` mounted, sdk ref `../../sdk/csharp`, workdir at module.
- `csharpModInitLegacyConfig` — same but legacy `dagger.json` (required for
  anything driving `dagger check`).
- `csharpModInitGit` — builtin `csharp` sdk name (fork git ref); required for
  tests loading through `host.directory().asModule()` (inspectModule), which
  cannot resolve relative sdk refs.

## Deferred items

All previously-deferred items are done (checks matrices, examples,
TestContextGit). Remaining nice-to-haves:

1. **daggerverse modules** (pjmagee/daggerverse): updated and working; a
   re-`dagger develop` would pick up auto-field exposure and the telemetry
   trace fix.
2. **NuGet package**: DaggerIO 0.1.0-preview.2 predates the current API;
   standalone-client uses a project reference until a fresh package ships.
3. ~~Analyzer DAGGER010~~ — done: [DefaultPath] now allows GitRepository and
   GitRef alongside Directory/File, matching the engine's contextual
   resolution; the TestContextGit fixture no longer needs a pragma.
4. ~~Record support (issue #10)~~ — done: `DaggerObjectAnalyzer` now analyzes
   `RecordDeclarationSyntax` alongside classes; reflection registration already
   handled records at runtime. Covered by `TestCsharp/TestRecord` and
   `DaggerObjectAnalyzerTests` record cases. `record struct` deliberately
   excluded.

Closed GitHub issues on 2026-07-04 (all implemented on this branch):
issues #4, #5, #6, #7, #8, #9, #11, #12, #13 (with evidence comments); #10
implemented here.
