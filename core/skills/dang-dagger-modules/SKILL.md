---
name: dang-dagger-modules
description: Authoring Dagger modules in Dang — dagger.json setup, the main type & constructor, what becomes module API, self-calls (supported; annotate returns as Dagger.<Type>!), dependencies, enums/interfaces/scalars, directives (@check, @cache, @defaultPath, @agent), Workspace args, and caching pitfalls. Use when writing or reviewing a Dagger module implemented in Dang.
---

# Dagger Modules in Dang

**Audience: people writing Dagger modules in Dang.** For the language itself
(syntax, nullability, copy-on-write mutation, control flow, GraphQL interop)
see the `dang-language` skill; this skill covers only what is specific to Dang
as a **Dagger module SDK**.

## Mental model

- A Dang module is a directory of `.dang` files plus a config naming `dang` as
  the SDK. **All** top-level `.dang` files in the source directory form one
  module (subdirectories and other files are ignored); declarations are
  order-independent across files.
- **No codegen, no container** — the engine interprets Dang natively. There is
  no generated client: the Dagger API is auto-imported under the `Dagger`
  namespace, and bare names work too (`container.from(...)` ≡
  `Dagger.container.from(...)`).
- Every **public top-level declaration** becomes module API: `type` → object
  (plus constructor), `enum` → enum, `interface` → interface. `let` keeps a
  binding private. Custom `scalar`s are exposed as strings.
- The **main object** is the type whose name matches the module name.
  Secondary types get module-prefixed schema names: `type Widget` in module
  `test` lives in the schema as `TestWidget`.
- **Self-calls are fully supported** (always enabled for the Dang SDK — no
  experimental flag needed): a module can call its own functions through the
  engine via its own root binding (`test.foo`, `tuiQa`). See the dedicated
  section — the return-type annotation rule is the #1 gotcha.

## Module setup

```json
{
  "name": "counter",
  "engineVersion": "v0.21.5",
  "sdk": { "source": "dang" }
}
```

- `"sdk": "dang"` (plain string) also works.
- `engineVersion` selects the Dang major: `< v0.21.5` routes to frozen Dang v1
  (where `.{ }` was GraphQL selection); `>= v0.21.5` is Dang v2 (`.{ }` is
  dot-block application, `.{{ }}` is selection). Don't bump an old module's
  `engineVersion` without checking its selection syntax.
- Dependencies: `"dependencies": [{"name": "gochild", "source": "gochild"}]` —
  deps may use any SDK (Go, Python, TypeScript, Dang) side by side.
- `"disableDefaultFunctionCaching": true` turns off default function caching
  module-wide (coarse alternative to per-function `@cache`).
- Legacy `"sdk": {"experimental": {"SELF_CALLS": true}}` is obsolete: self-calls
  graduated to a runtime-capability check and are always on for Dang.

## Main type, constructor & API surface

```dang
type Greeter {
  let secret: String! = "hidden"          # private state, never exposed
  pub name: String!                        # data field (also a ctor param)

  new(name: String! = "world") {           # explicit constructor
    self.name = name.capitalize
    self                                   # must end with self
  }

  pub greet: String! { "Hey, " + name }    # computed field / zero-arg function
}
```

- Without `new`, the uninitialized public fields form an implicit constructor
  in declaration order (positional construction works).
- Constructor args become top-level flags: `dagger call --name alice greet`.
  Arg names need not match field names; the body really executes.
- `pub` and bare typed declarations are exposed; `let` is private — including
  `let` *functions*, the idiom for internal helpers.
- A field with a body is a function/computed field; a plain typed field is
  data. Docstrings (`"""..."""` before a declaration or arg) become API
  descriptions.
- Private (`let`) fields persist across calls: they serialize into the
  object's state and rehydrate on the next call. Chain state mutations
  copy-on-write style: `with(x): Self! { self.x = x; self }`.
- Non-null args (`T!`) are required; nullable args are optional — the idiom
  for optional inputs is `arg: File = null`.
- `Map[...]` **cannot** be exposed through the API; keep maps in `let` fields
  (they serialize fine privately). Ad-hoc record types can't be exposed
  either — declare a named `type`.
- `Void` return marks an effect-only function; end the body with `null` (or a
  `Void`-typed call), and use `.sync` to force container execution.

## Self-calls

A self-call invokes the module's **own** API through the engine, via the
module's root binding (the module name in camelCase):

```dang
type Test {
  pub containerEcho(msg: String!): Container! {
    container.from("alpine").withExec(["echo", msg])
  }

  pub print(msg: String!): String! {
    test.containerEcho(msg: msg).stdout    # self-call
  }

  pub fresh: Dagger.Test! { test }         # self-call the constructor
}
```

- **Return-type annotation rule:** a function that *returns* a self-call
  result must declare the return as `Dagger.<SchemaTypeName>!` — e.g.
  `Dagger.Test!`, or `Dagger.TestWidget!` for a secondary `Widget` — not the
  bare local type. A self-call yields the type *as installed in the runtime
  schema* (namespaced, carrying a GraphQL id); the bare local type is a
  different type. Annotating with the bare type makes the runtime receive a
  raw ID string where an object is expected.
- Constructing locally (`Widget(x)`) yields the bare local `Widget!`; only an
  actual API call yields `Dagger.TestWidget!`.
- Self-call results are real objects: fields read back fine
  (`test.fresh.getMessage`), including on secondary types.
- Self-calls also work when the module is used as a dependency, transitively.
- Bare `test` (zero-arg constructor auto-call) is the way to reset to a fresh
  instance from inside a method.

## Dependencies

- A dependency named `foo` is callable as the root binding `foo`:
  `foo.curve(...)`, `dangchild.value`. Deps with constructor args are called
  like functions: `engineDev(ws: source).test`.
- A dependency's types appear module-namespaced: dep `foo`'s `enum EcCurve`
  is `FooEcCurve`, dep `dep`'s `interface Greeter` is `DepGreeter`.

## Enums, interfaces, scalars

- `enum Status { PENDING RUNNING DONE }` — compare with `==`; CLI passes
  members verbatim (`--status DONE`).
- `interface Local { pub greet(name: String!): String! }` plus
  `type Hey implements Local` — implementers must **not** declare the
  synthesized `id: ID!` field Dagger adds to every interface.
- Structural conformance crosses module boundaries: an object matching a dep
  interface's shape passes as that interface without `implements`.
- Interface methods touching core types annotate them qualified:
  `apply(container: Dagger.Container!): Dagger.Container!`.
- `scalar Timestamp` is exposed as a String at the boundary; values arrive as
  strings.

## Directives Dagger consumes

- Function-level: `@check` (marks a check; typically on `Void` returns),
  `@generate` (on Changeset-returning generators), `@up` (on `Service!`),
  `@agent` (see below), `@cache`.
- Arg-level: `@defaultPath(path: ...)` on `Directory!` args — relative paths
  resolve against the module, `"/"` against the context root;
  `@ignorePatterns(patterns: [...])` filters with gitignore-style patterns
  (allowlisting via `"!keep"` works). Positional and named args both parse.
- Placement: suffix (`screen: String! @cache(...)`) or prefix on the line
  before the declaration.
- Agent idiom:

  ```dang
  agent(base: LLM!): LLM! @agent {
    base.withTools(currentNode).withSystemPrompt(systemPrompt)
  }
  ```

## Workspace args

- A `Workspace!`-typed arg (bare or `Dagger.Workspace!`) is auto-filled by the
  caller's workspace — no flag needed on `dagger call`; for agents it's filled
  from the bound workspace and hidden from the model.
- `let ws: Workspace!` as an uninitialized field is the standard pattern for
  holding it. Read with `ws.file(...)`, `ws.directory(path, exclude: [...])`.
- The mounted workspace is a plain snapshot with **no `.git`** — `git diff`
  won't work; use `Workspace.git.uncommitted` (a Changeset) with
  `.diffStats` / `.asPatch`.

## Caching pitfalls

- The engine memoizes function results by (object id, field, args) within a
  session. Side-effecting or live-reading functions **must** opt out:
  `@cache(policy: FunctionCachePolicy.Never)` (mixes a per-call nonce into the
  call id). `@cache(ttl: 300)` sets a time-to-live instead.
- Even with `Never`, identical container execs still hit the exec cache — bust
  with a nonce: `.withEnvVariable("NONCE", Random.string)`.

## Shadowing core types

- A module may declare types shadowing core names (`type Container`); the bare
  name then means the local type, and `Dagger.Container!` / `Dagger.container`
  disambiguates back to core.

## Pitfalls checklist

- Self-call return annotated with the bare local type instead of
  `Dagger.<T>!` → runtime gets a raw ID string. (Self-calls DO work — don't
  conclude otherwise from old comments.)
- Missing `@cache(policy: FunctionCachePolicy.Never)` on a stateful/live tool
  → the second call replays the first result.
- `pub`-exposing a `Map[...]` or an ad-hoc record type → hard error.
- Declaring `id` when implementing a dep interface → error; omit it.
- Using v1 `.{ }` selection in a `>= v0.21.5` module — that's dot-block now;
  select with `.{{ }}`.
- Only *top-level* type declarations become module types; types defined inside
  bodies aren't hoisted into the schema.
