# Identifier Casing

Status: proposal

Tracking: [dagger/dagger#13668 (comment)](https://github.com/dagger/dagger/issues/13668#issuecomment-5939391182)

## Table of Contents

- [Summary](#summary)
- [Problem](#problem)
- [Goals](#goals)
- [Non-Goals](#non-goals)
- [Terminology](#terminology)
- [Parsing](#parsing)
- [Formatting](#formatting)
- [The Dictionary](#the-dictionary)
- [Core API](#core-api)
- [Engine Integration](#engine-integration)
- [SDK Integration](#sdk-integration)
- [Versioning and Compatibility](#versioning-and-compatibility)
- [Guarantees](#guarantees)
- [Known Limitations](#known-limitations)
- [Test Vectors](#test-vectors)
- [Open Questions](#open-questions)
- [SDK Extraction](#sdk-extraction)
- [Implementation Plan](#implementation-plan)

## Summary

Case conversion moves into the engine and is exposed as a core API. An
identifier is parsed once into a list of **words**, each tagged as an ordinary
word, an **acronym** (`HTTP`), or a **term** with a fixed spelling (`GitHub`,
`IPv6`). The parsed identifier can then be formatted in any casing a language
uses (`PASCAL`, `CAMEL`, `SNAKE`, `SCREAMING_SNAKE`, `KEBAB`, `FLAT`), with an
**acronym style** that picks `HTTPClient` or `HttpClient`.

Parsing uses a central **dictionary** of acronyms and terms first, and falls
back to a case heuristic for everything else. The dictionary is versioned with
the engine, so adding a term never renames anything in a module that hasn't
opted into the new engine version.

The same code path produces schema names for module types, fields, arguments
and enum values, CLI flag names, and SDK codegen names. That replaces strcase
in the engine and the separate implementations in each SDK.

## Problem

Every layer that converts names makes its own guess about acronyms, and the
guesses disagree.

**The engine** normalizes module-declared names with
`github.com/iancoleman/strcase` (`core/gqlformat.go`). `strcase.ToCamel`
lowercases every run of capitals unless the *whole* name is a registered
acronym:

| input | `strcase.ToCamel` |
| --- | --- |
| `LLMContentBlock` | `LlmcontentBlock` |
| `JSONValue` | `Jsonvalue` |
| `MyMod` + `HTTPClient` | `MyModHttpclient` |
| `ModuleAOverlay` | `ModuleAoverlay` |
| `E2ETest` | `E2Etest` (and `ToSnake` gives `e_2_e_test`) |

Effects:

- A module that references a core type whose name starts with an acronym gets
  a different, module-namespaced type. `core/llm.go` works around this with one
  `strcase.ConfigureAcronym` line per affected core type, found one at a time.
- Normalization doesn't leave valid PascalCase names alone, so re-normalizing a
  final name corrupts it. `withFinalTypeName` / `__withName` in
  `core/typedef_results.go` exist only to work around that.
- User module types get mangled schema names (`MyModHttpclient`).

**SDK codegen** has at least seven independent implementations:

| SDK | implementation | example output |
| --- | --- | --- |
| Go | golint `lintName` + `commonInitialisms` (no SHA, VCS or GPU) | `ParentShas`, `VcsGeneratedPaths` |
| TypeScript | regex + an `LLM`-only acronym map | |
| Python | `ACRONYM_RE` + graphql-core `camel_to_snake` | `prerequisite_sh_as`, `experimental_with_all_gp_us` |
| Rust | own conversion | `prerequisite_sh_as`, `JsonValue` |
| Elixir | `Macro.underscore` + a GPU/VCS special case (#6310) | `prerequisite_sh_as`, but `experimental_with_all_gpus` |
| PHP | own conversion | `JsonValue` |
| .NET | regex + `ToTitleCase` | `InsecureSkipTlsverify` |

The same core type is `JSONValue` in Go, TypeScript, Python and Elixir, and
`JsonValue` in Rust and PHP.

**The core schema** isn't consistent either: `traceURL` but `pushUrl`,
`commitSHA` but `shortSha`, `prerequisiteSHAs` but `parentShas`.

The root cause: PascalCase, camelCase and all-lowercase encodings lose
information. `HTTPAPIClient` doesn't say where `HTTP` ends, `httpClient` doesn't
say `http` is an acronym, and `IPv6Address` looks like `I` + `Pv6` + `Address`.
Each implementation recovers that information with different heuristics.

## Goals

1. One implementation, in the engine, used for every name the engine produces
   and exposed to SDKs through the core API.
2. Support every casing languages use, plus both ways of writing acronyms
   (`HTTPClient` and `HttpClient`).
3. Names covered by the dictionary keep the same words through every casing
   and back.
4. Formatting is idempotent, and a name that is already canonical comes back
   unchanged. No more `withFinalTypeName`.
5. Fix the core-type acronym bug class for good, without per-type workarounds.
6. Changes to behavior or the dictionary never rename anything for a module that
   hasn't moved to the engine version that introduced them.

## Non-Goals

- Recovering acronyms the dictionary doesn't know once they've passed through a
  lowercase form. `httpx_client` becomes `HttpxClient` until someone adds `HTTPX`.
- Non-ASCII identifiers. GraphQL names are ASCII-only; non-ASCII input is an
  error.
- Per-module dictionary entries (see [The Dictionary](#the-dictionary)).
- .NET's two-letter acronym rule (see [Open Questions](#open-questions)).
- Automatically renaming core-defined schema names. Core names are written by
  hand in Go; see [Versioning and Compatibility](#versioning-and-compatibility).

## Terminology

| Term | Meaning |
| --- | --- |
| Identifier | A name parsed into an ordered list of words. |
| Word | One unit of an identifier: kind, text, suffix. |
| `WORD` | An ordinary word, stored lowercase: `client`. |
| `ACRONYM` | A word written in capitals: `HTTP`, `E2E`, `3D`. Comes from the dictionary or from a run of capitals in the input. |
| `TERM` | A dictionary entry with a fixed mixed-case spelling: `GitHub`, `IPv6`, `iOS`. |
| Suffix | A plural `s` and/or trailing digits attached to an acronym or term: `SHA`+`s`, `OAuth`+`2`, `HTTP`+`2`. An ordinary word keeps its digits in its text. |
| Casing | The joining convention: `PASCAL`, `CAMEL`, `SNAKE`, `SCREAMING_SNAKE`, `KEBAB`, `FLAT`. |
| Acronym style | How acronyms and terms are written where a word starts with a capital: `UPPERCASE` (`HTTPClient`) or `CAPITALIZED` (`HttpClient`). |
| Canonical name | `format(parse(x), casing, UPPERCASE)` for the casing the schema uses at that position. |
| Dictionary | The engine's versioned list of acronyms and terms. |

## Parsing

Parsing turns a string into words. Steps 1–3 split the input using its own
structure, step 4 picks words using the dictionary with the heuristic as
fallback, and steps 5–6 tidy up.

### 1. Validate and chunk

- Reject non-ASCII input and input with no letters or digits.
- Split on every non-alphanumeric character (`_`, `-`, `.`, space, ...). These
  are hard word boundaries; empty chunks are dropped.

### 2. Case mode

If the input contains both uppercase and lowercase letters, it is **cased**.
Otherwise (all caps, like `HTTP_CLIENT`, or all lowercase, like `http_client`)
it is **caseless**: case carries no information, so every chunk is lowercased
and taken as one piece in step 3. Without this rule, `HTTP_CLIENT` would turn
`CLIENT` into an acronym.

All-caps input still says one thing: each chunk is a run of capitals, with no
boundaries between adjacent acronyms (`JSONAPI`, `HTMLURL`). So when the input
is all caps, every chunk is also eligible for the full dictionary cover that
caps pieces get in step 4: `JSONAPI` → `JSON`·`API`, but `HTTP_CLIENT` stays
`HTTP`·`client` and `HTTPX_CLIENT` stays `httpx`·`client`, because `CLIENT` and
`HTTPX` can't be covered completely. All-lowercase input never splits.

### 3. Pieces and digit cuts

In cased mode, each chunk is split into **pieces** at case boundaries. There is
a boundary before position `k` when either:

- **(a)** `k` is a capital, and the nearest earlier character that isn't a
  digit is lowercase: `httpClient` → `http|Client`, `Get3DModel` →
  `Get3|DModel`, `Base64URLEncode` → `Base64|URLEncode`.
- **(b)** `k` is a capital followed by a lowercase letter, and preceded by a
  capital or digit. The last capital of a run starts the next word:
  `HTTPClient` → `HTTP|Client`, `E2ETest` → `E2E|Test`, `DModel` → `D|Model`.
  - **Plural exception:** no boundary if that lowercase letter is an `s` that
    ends the chunk or is followed by a capital or digit. `prerequisiteSHAs` →
    `prerequisite|SHAs`, `IDsFoo` → `IDs|Foo`, `listPRs` → `list|PRs`.

A piece with no lowercase letters (ignoring a trailing plural `s` on a piece of
three or more characters) is a **caps piece**: `HTTP`, `E2E`, `SHAs`,
`HTTPAPI`.

Every piece is then cut further at each letter/digit change, for dictionary
matching only: `Get3` → `Get`, `3`; `Pv6` → `Pv`, `6`; `e2e` → `e`, `2`, `e`.
These cuts don't become word boundaries unless step 4 uses them.

### 4. Dictionary segmentation

Words are chosen by searching for the best way to divide the cut sequence.
Each candidate word is one of:

1. **Dictionary match:** one or more consecutive cut segments, possibly crossing
   piece boundaries, whose concatenation equals an entry's spelling
   (case-insensitively), or the spelling plus `s`.
   - `I`+`Pv`+`6` → `IPv6`; `Git`+`Hub` → `GitHub`; `3`+`D` → `3D`; `e`+`2`+`e`
     → `E2E`; `Ids` → `ID`+`s`.
2. **Caps piece cover:** a whole caps piece, or a whole chunk of all-caps input
   (step 2), written as a sequence of dictionary entries and digit runs.
   `HTTPAPI` → `HTTP`+`API`; `HTTP2` → `HTTP`+`2`; `APIs` → `API`+`s` (only
   the last entry can take the piece's plural `s`). This is the only place the
   dictionary looks *inside* a segment, because a run of capitals has no
   internal boundary information. If the cover isn't complete (`HTTPX`), this
   candidate doesn't apply.
3. **Heuristic word:** consecutive cut segments within one piece, as a single
   word.

The chosen division is the one that is best by, in order:

1. the fewest letters not covered by a dictionary entry,
2. then the fewest words (digit-only words don't count; they're glued in step 5),
3. then the fewest plural matches (so `ios` is `iOS`, not `IO`+`s`),
4. then the fewest words counting digit-only ones, so a heuristic word keeps
   its digits (`ABC2`, not `ABC`·`2`).

**Why matches must line up with boundaries.** Dictionary matching only
compares whole cut segments, never arbitrary substrings. That's what keeps
`Capital` from containing `API` and `Identity` from containing `ID`. Lowercase
segments are never split internally. The only places inside a word where a
dictionary entry can apply are a letter/digit cut (`get3d` → `get` + `3D`) and
a run of capitals that entries cover completely.

### 5. Digit gluing

A word made only of digits is appended to the previous word: to an acronym's
or term's suffix (`OAuth`·`2` → `OAuth`+`2`, `sha256` → `SHA`+`256`), or to an
ordinary word's text (`foo_2` → `foo2`), so a `WORD` always carries its digits
in its text, like `base64`. Digits that start the input stay a word of their
own.

So **digits belong to the word before them by default** (`base64`, `int32`,
`v1`, `beta1`, `win32`), and dictionary entries decide the exceptions (`3D`,
`E2E`).

### 6. Word kinds

| source | kind | stored text |
| --- | --- | --- |
| dictionary entry with no lowercase letters | `ACRONYM` | entry spelling: `HTTP` |
| dictionary entry with lowercase letters | `TERM` | entry spelling: `GitHub` |
| heuristic, from a caps piece with ≥ 2 letters | `ACRONYM` | as written, trailing plural `s` moved to suffix: `HTTPX`, `PR`+`s` |
| any other heuristic word | `WORD` | lowercased: `client` |

A single capital (`A` in `ModuleAOverlay`) is a `WORD`; it formats the same
either way.

### Dictionary spelling wins over author casing

Matching ignores case, and a match always takes the entry's spelling. So
`HttpClient`, `httpClient`, `http_client` and `HTTPClient` all parse to `HTTP` ·
`client`; `Github` and `GITHUB` parse to `GitHub`; `Sha256Sum` parses to
`SHA`+`256` · `sum`. This makes the schema look the same whichever language a
module was written in: a Ruby `HttpClient` and a Go `HTTPClient` are the same
type.

## Formatting

| casing | rule | `HTTP`·`API`·`client` | `GitHub`·`repo` |
| --- | --- | --- | --- |
| `PASCAL` | every word capitalized-form, joined | `HTTPAPIClient` | `GitHubRepo` |
| `CAMEL` | first word lowercase, rest capitalized-form | `httpAPIClient` | `githubRepo` |
| `SNAKE` | lowercase, `_` | `http_api_client` | `github_repo` |
| `SCREAMING_SNAKE` | uppercase, `_` | `HTTP_API_CLIENT` | `GITHUB_REPO` |
| `KEBAB` | lowercase, `-` | `http-api-client` | `github-repo` |
| `FLAT` | lowercase, no separator | `httpapiclient` | `githubrepo` |

**Capitalized form** of a word, used by `PASCAL` and the non-first words of
`CAMEL`:

| kind | `UPPERCASE` | `CAPITALIZED` |
| --- | --- | --- |
| `WORD` | `Client` | `Client` |
| `ACRONYM` | `HTTP` | entry's `capitalized` (`Http`); heuristic acronyms: first letter capital, rest lowercase (`Httpx`) |
| `TERM` | spelling with the first letter capitalized (`GitHub`, `IPv6`, `IOS`) | entry's `capitalized` (`GitHub`, `Ipv6`, `Ios`) |

The suffix is appended as is (lowercase `s`, digits), except in
`SCREAMING_SNAKE`, where everything is uppercase. `acronyms` has no effect on
`SNAKE`, `SCREAMING_SNAKE`, `KEBAB` or `FLAT`.

`FLAT` exists for Go package names and Python module names. It drops all
boundaries, so it is output-only: it doesn't round-trip.

## The Dictionary

### Entry shape

```text
spelling     the standard spelling:            HTTP   IPv6   GitHub   iOS   3D
capitalized  spelling in the CAPITALIZED style: Http   Ipv6   GitHub   Ios   3D
```

`capitalized` is stored per entry because it can't be derived. Rust writes
`Ipv6Addr` and `Grpc`, but a brand like `GitHub` never changes. For plain
acronyms it defaults to the first letter capitalized and the rest lowercase.

Plurals are automatic (`SHAs`, `IDs`, `GPUs`); there are no plural entries.

### What belongs in it

- **Acronyms and initialisms:** `HTTP`, `JSON`, `LLM`, `SHA`, `GPU`. Follow
  golint's rule: only add entries "highly unlikely to be non-initialisms". `ID`
  is fine; `AND` is not; be wary of `IT`, `AS`, `OR`.
- **Mixed-case terms** the heuristic would split: `GitHub`, `GitLab`, `OAuth`,
  `IPv4`, `IPv6`, `iOS`, `macOS`, `gRPC`, `GraphQL`.
- **Digit-led terms** the default digit rule would split: `3D`, `2D`.
- **Adjacent-acronym support is free:** with `HTTP` and `API` present,
  `HTTPAPI` splits correctly. No `HTTPAPI` entry is needed.

Entries are *not* needed for digit suffixes: `OAuth` covers `OAuth2`, `HTTP`
covers `HTTP2`, `SHA` covers `SHA256`.

### Initial contents

- golint's `commonInitialisms` (what the Go SDK uses today): `ACL API ASCII CPU
  CSS DNS EOF GUID HTML HTTP HTTPS ID IP JSON LHS QPS RAM RHS RPC SLA SMTP SQL
  SSH TCP TLS TTL UDP UI UID UUID URI URL UTF8 VM XML XMPP XSRF XSS`
- The Go SDK's Dagger additions: `FS SDK LLM`
- Acronyms found in the core schema: `GPU VCS SHA MCP OCI SSHFS`
- Common in modules: `E2E CLI TUI IO PR EC2 MD5`
- Terms: `GitHub GitLab OAuth IPv4 IPv6 iOS macOS gRPC GraphQL 3D 2D`. Their
  `capitalized` forms are `Oauth Ipv4 Ipv6 Ios MacOS Grpc`; the brands
  `GitHub GitLab GraphQL` and `3D 2D` keep their spelling.

### Process

New entries are requested in an issue or PR and ship with the next engine
release. Each addition renames names for anyone who already has them (a Python
`httpx_client` goes from `HttpxClient` to `HTTPXClient`), so additions follow
the engine-version gate in
[Versioning and Compatibility](#versioning-and-compatibility).

### No per-module entries

A module's schema names are re-parsed by *consumers* with the core dictionary.
A term only that module knows would be lost there, so all entries live in the
central list.

## Core API

```graphql
extend type Query {
  """
  Parse a name in any casing into words. Known acronyms and terms come from
  the naming dictionary; everything else falls back to the case heuristic.
  Errors on non-ASCII input or input with no letters or digits.
  """
  identifier(name: String!): Identifier!

  """
  Format many names at once, for codegen. Returns them in input order.
  """
  formatIdentifiers(
    names: [String!]!
    casing: Casing!
    acronyms: AcronymStyle = UPPERCASE
    """
    The engine version whose naming dictionary to parse the names with, e.g.
    the __schemaVersion of a schema being generated. Defaults to the caller's
    engine version.
    """
    version: String
  ): [String!]!

  """The acronyms and terms used to parse and format identifiers."""
  namingDictionary: [NamingTerm!]!
}

"""A name parsed into words, which can be formatted in any casing."""
type Identifier implements Node {
  id: ID!

  """The name as given."""
  name: String!

  """The words that make up the name, in order."""
  words: [IdentifierWord!]!

  """Format the identifier in a casing."""
  format(casing: Casing!, acronyms: AcronymStyle = UPPERCASE): String!
}

"""One word of an identifier."""
type IdentifierWord {
  """Standard spelling: "client" (WORD), "HTTP" (ACRONYM), "GitHub" (TERM)."""
  text: String!

  """A plural "s" and/or trailing digits: SHA+"s", OAuth+"2"."""
  suffix: String!

  kind: IdentifierWordKind!

  """The dictionary entry this word matched, if any."""
  term: NamingTerm
}

enum IdentifierWordKind {
  """An ordinary word."""
  WORD

  """An acronym, from the dictionary or a run of capitals."""
  ACRONYM

  """A dictionary term with a fixed mixed-case spelling (GitHub, IPv6)."""
  TERM
}

"""An entry in the naming dictionary."""
type NamingTerm {
  """Standard spelling: "HTTP", "IPv6", "GitHub", "iOS"."""
  spelling: String!

  """Spelling in the CAPITALIZED style: "Http", "Ipv6", "GitHub", "Ios"."""
  capitalized: String!
}

"""A convention for joining words into an identifier."""
enum Casing {
  """HTTPClient"""
  PASCAL

  """httpClient"""
  CAMEL

  """http_client"""
  SNAKE

  """HTTP_CLIENT"""
  SCREAMING_SNAKE

  """http-client"""
  KEBAB

  """httpclient (output only: drops word boundaries)"""
  FLAT
}

"""How acronyms and terms are written where a word starts with a capital."""
enum AcronymStyle {
  """HTTPClient, IPv6Address, GitHubRepo"""
  UPPERCASE

  """HttpClient, Ipv6Address, GitHubRepo"""
  CAPITALIZED
}
```

Notes:

- `formatIdentifiers` returns scalars on purpose. SDK clients load a list of
  objects one ID at a time, so a list of `Identifier`s would cost a round trip
  per name. Codegen formats thousands of names.
- `identifier` and `namingDictionary` follow the caller's engine version, like
  the rest of the schema. So does `formatIdentifiers`, unless given a
  `version`: codegen passes the `__schemaVersion` of the schema it generates,
  so a module's names are parsed with the dictionary of the module's engine
  version, whatever version the codegen connection is served at.
- `formatIdentifiers` isn't cached: it's pure and fast, and caching costs more
  than formatting (see
  [Formatting names in codegen](#formatting-names-in-codegen)).
- `Casing` and `AcronymStyle` can gain values later without breaking anyone.
- Like every object in the schema, `IdentifierWord` and `NamingTerm` also
  implement `Node`.
- The API is `@experimental` and visible to clients at engine version
  `v1.0.0-0` and above, so prereleases see it, except that
  `formatIdentifiers` is hidden from the views of modules that keep the legacy
  naming rules (see [The gate](#the-gate)).

## Engine Integration

All engine-side case conversion goes through `engine/naming`, which the core
API also wraps. In `core`, a `Namer` (`core/gqlformat.go`) holds either a
naming dictionary or nothing; a nil dictionary is the legacy path, which keeps
the old strcase code byte for byte:

| legacy (strcase) | `Namer` with a dictionary |
| --- | --- |
| `gqlObjectName` (`strcase.ToCamel`) | `ObjectName`: `PASCAL` / `UPPERCASE` |
| `gqlFieldName`, `gqlArgName` (`strcase.ToLowerCamel`) | `FieldName`, `ArgName`: `CAMEL` / `UPPERCASE` |
| `gqlEnumMemberName` (`strcase.ToScreamingSnake`) | `EnumMemberName`: `SCREAMING_SNAKE` |
| `namespaceObject` prefix check (`'A' <= rest[0] <= 'Z'`) | `NamespaceObject`: the object's words start with the module's words |
| `strcase.ToKebab` for CLI paths and addresses (`core/modtree.go`, `core/artifacts.go`, `core/schema/address.go`, `engine/server/session_workspaces.go`) | `CLIName`: `KEBAB`, keeping glob and path characters |
| `strcase.ConfigureAcronym` in `core/llm.go`, `core/json.go` | dictionary; the calls stay for the legacy path only |
| `withFinalTypeName` / `__withName` in `core/typedef_results.go` | not needed on the new path (canonical names are fixed points); kept for legacy modules |

### Which namer applies

- **Typedef constructors** (`function`, `withArg`, `__objectTypeDef`,
  `withEnum`, `__withCollectionMember`, ...) are installed with
  `View(AllVersion)`, so the caller's view is part of the call and its cache
  key, and they normalize with `NamerFromContext` (the namer for the current
  call's view). A module runtime's client is served at the module's view
  (`ModuleSource.NamingView`), so its typedefs are named by the module's
  rules. Public constructors pass the view on to the internal `__*` selects
  they make; the in-engine Dang runtime stamps its module's view on its naming
  selectors (`core/sdk/dang/shared/naming.go`). A module that keeps the legacy
  rules at a view that has the new ones is served at that view marked for
  legacy naming (see [The gate](#the-gate)).
- **Module-level naming** (`namespaceObject`, type and constructor lookups,
  validation) uses `Module.Namer()`, chosen by the module source's
  `engineVersion` (`ModuleSource.Namer`; legacy for a module entrypoint).
  Module tree CLI paths (checks, generators, artifacts) use
  the namer of the module that declared the node, so a legacy dependency keeps
  its old paths inside a new workspace.
- **Mixed versions.** A dependency's types are installed with *its* rules, so
  a module at a different version may spell a reference differently
  (`HttpClient` vs `HTTPClient`). `namespaceTypeName` falls back to a
  dependency type whose name matches under either rule set
  (`depTypeReference`). Legacy modules skip core types in that fallback, so
  their schema doesn't change.
- **Matching user input.** Addresses, include patterns and pre-load module
  name matching (`FieldNameCandidates`, `SameCLIName`, `artifactPatterns`)
  accept both the legacy and the new spelling, since they run before the
  module's version is known.
- **SDK runtime arguments.** SDK modules at the new version receive the
  `introspectionJSON` argument instead of `introspectionJson`.
- **Go module codegen** (`cmd/codegen`) can't import `core`, so
  `module_naming.go` mirrors `Namer`. It gates like SDK codegen, on
  `Query.formatIdentifiers` being in the schema it generates from, so it can't
  disagree with the engine (see [The gate](#the-gate)).

### CLI

The CLI loads typedefs without each module's `engineVersion`, so it names
commands and flags with the dictionary for its own engine version. Legacy
spellings stay accepted: flags are normalized through both spellings, and
commands get the legacy name as an alias. So `E2ETest` is `--e2e-test`, and
`--e-2-e-test` still works.

### Remaining strcase uses

- The legacy `Namer` path and `legacyNamespaceObject`, for modules below the
  gate.
- `canonicalWorkspaceModuleName` and `canonicalOverlayModuleName`: comparison
  keys only, never shown to users.
- The `core/llm.go` and `core/json.go` acronym registrations, which the legacy
  path still needs.
- Legacy fallbacks in the CLI and `core/envfile.go` (old flag and variable
  prefixes).
- `dagql` struct-field naming and `dagql/idtui` display, which handle core
  names, not module names.

## SDK Integration

Codegen asks the engine to format the schema's names, with
`Query.formatIdentifiers` (see
[Formatting names in codegen](#formatting-names-in-codegen)), so the formatting
rules and the dictionary live only in the engine. Each SDK keeps its old
converter only as the fallback for schemas without `formatIdentifiers`, so
older schemas generate byte-identical code.

The principle is **"when in Rome"**: each SDK follows its own language's
acronym convention, not one house style. Go writes initialisms in capitals
(`ParentSHAs`), .NET, Java and PHP write acronyms like words (`WithMcpServer`,
`asJson`, `withGpu`), and the snake-case SDKs need only the word boundaries
(`prerequisite_shas`). What each SDK generates:

| SDK | types | methods / fields | args | enum values |
| --- | --- | --- | --- | --- |
| GraphQL schema | `PASCAL`/`UPPERCASE` | `CAMEL`/`UPPERCASE` | `CAMEL`/`UPPERCASE` | `SCREAMING_SNAKE` |
| Go | `PASCAL`/`UPPERCASE` | `PASCAL`/`UPPERCASE` | `CAMEL`/`UPPERCASE` | `PASCAL`/`UPPERCASE` |
| TypeScript | schema name (`PASCAL`/`UPPERCASE`) | `CAMEL`/`UPPERCASE` | `CAMEL`/`UPPERCASE` | `PASCAL`/`CAPITALIZED` |
| Python | schema name | `SNAKE` | `SNAKE` | `SCREAMING_SNAKE` (value: schema value) |
| Elixir | `PASCAL`/`UPPERCASE` | `SNAKE` | `SNAKE` | schema-value atoms (`SNAKE` functions) |
| Rust | `PASCAL`/`CAPITALIZED` | `SNAKE` | `SNAKE` | `PASCAL`/`CAPITALIZED` |
| PHP | `PASCAL`/`CAPITALIZED` (`#[GraphQLType]`) | `CAMEL`/`CAPITALIZED` | `CAMEL`/`CAPITALIZED` | `SCREAMING_SNAKE` |
| .NET | `PASCAL`/`CAPITALIZED` | `PASCAL`/`CAPITALIZED` | `CAMEL`/`CAPITALIZED` | `PASCAL`/`CAPITALIZED` |
| Java | `PASCAL`/`CAPITALIZED` | `CAMEL`/`CAPITALIZED` | `CAMEL`/`CAPITALIZED` | `SCREAMING_SNAKE` |

Only SDK identifiers change. Selected fields, argument names, input object
keys, enum values and GraphQL type names go over the wire as the schema has
them. Where an SDK serializes an input object field by its own name, a
renamed field is mapped back to its wire name (`#[serde(rename)]` in Rust,
`@JsonbProperty` in Java; Elixir keeps the legacy struct keys).

Type names stay as the schema has them in SDKs whose runtime looks types up by
name (TypeScript, Python). Go, Rust and Elixir already write them by their own
conventions; PHP, Java and .NET name types and enum members by their own
conventions too, and carry the GraphQL names the wire needs alongside (see
below).

Python names enum members `SCREAMING_SNAKE`, as PEP 8 writes constants
(`FunctionCachePolicy.PER_SESSION`, `ImageLayerCompression.GZIP`). A member's
value is the schema value it stands for, which goes over the wire: such
enums extend `ValueEnum`, which the client sends by value rather than by
member name. The schema's other spellings of a value (the legacy
`PerSession`, `Gzip`, `OCIMediaTypes`) stay as deprecated aliases, so old code
keeps working and a value the API returns is still found by name. The module
runtime also sends the module's own enum members by their schema names in
calls through its interfaces (the engine names them `SCREAMING_SNAKE`).

TypeScript's own convention is ambiguous (`XMLHttpRequest`, `innerHTML`).
Members follow the schema's `UPPERCASE` (`filterURI`, `callID`), and enum
members keep the `PASCAL`/`CAPITALIZED` form the generator always wrote
(`Tcp`, `Oci`).

.NET follows the Framework Design Guidelines: types, methods, input object
properties and enum members are PascalCase and parameters camelCase, with
acronyms written like words (`JsonValue`, `Llm`, `WithMcpServer`,
`insecureSkipTlsVerify`, `Tcp`), all from the names file's `PASCAL:CAPITALIZED`
and `CAMEL:CAPITALIZED`. The guidelines' two-letter exception (`IOStream`) is
left out: the only two-letter acronym in the core schema besides `ID` (which the
guidelines write `Id`) is `GZ`, in an enum value, so it changes no C# name.
Generated code writes GraphQL type names as literals (node lookups, inline
fragments), so renamed types are safe on the wire. Enum members carry the schema
values they stand for in `[GraphQLValue("TCP")]`, which `GraphQLEnumConverter<T>`
and `GraphQLEnum.ToValue` read for results, arguments and input fields
(System.Text.Json 8 has no per-member names); a value and its alias share a
member (`Gzip` for `Gzip` and `GZIP`). Renamed methods and properties keep their
old names as `[Obsolete]` forwarders (default interface methods on interfaces),
and renamed enum members as `[Obsolete]` aliases (`TCP = Tcp`). Renamed
parameters can't be aliased, since C# can't overload on parameter names, and
neither can renamed types: enums, input structs and return types can't be
aliased across assemblies, and an obsolete subclass would only work where the
old type is passed in, not where it's returned.

Java follows Google Java Style: classes are PascalCase with acronyms written
like words (`JsonValue`, `Llm`, `Json`, `Id`), members camelCase likewise
(`asJson`, `withGpu`), and enum constants `SCREAMING_SNAKE` (`PER_SESSION`,
`E_STAR_GZ`), from the names file's `PASCAL:CAPITALIZED`, `CAMEL:CAPITALIZED`
and `SCREAMING_SNAKE:UPPERCASE` (or one aliased `formatIdentifiers` request when
the codegen plugin asks the engine). A class named apart from its GraphQL type
carries `@GraphQLType("JSONValue")`, which `Client.loadObjectFromID` and the
module annotation processor read; everything else already sends the type name
as a literal. An enum whose constants are named apart from its values implements
`GraphQLEnum`: each constant carries its schema values (a value and its alias
share one, `GZIP("Gzip", "GZIP")`), and `GraphQLEnums`, `JsonConverter`,
`Arguments`, `QueryBuilder` and the enum's JSON-B (de)serializers convert by
value. Renamed members keep their old spellings as deprecated forwarders and
renamed enum constants as deprecated static aliases
(`FunctionCachePolicy.PerSession`), but a class renamed only in case can't keep
a deprecated alias next to it on case-insensitive file systems (`JSON.java` and
`Json.java`), so class renames are hard breaks. The annotation processor finds
the ID and JSON scalar classes under either spelling (`Id` or `ID`, `Json` or
`JSON`), since it is compiled against SDKs generated from old and new schemas
alike.

PHP follows the Symfony and Laravel convention of writing acronyms like words
in members (`filterUri`, `withGpu`) and PSR-1's in classes (`Llm`,
`SdkConfig`, `HttpState`, `JsonValue`). PHP method names are case-insensitive,
so member changes are cosmetic; parameter names matter for named arguments.
Class names are case-insensitive too, so a class renamed in letter case only
is still the same class under its old spelling; but PSR-4 autoloading looks
for the old file name, which a case-sensitive filesystem doesn't find, and a
`class_alias` can't help, since to PHP both spellings are the same name. The
SDK registers `LegacyClassNames` after Composer's autoloader: it finds the
generated file ignoring case, and reports the old spelling as deprecated. A
class whose name differs from its GraphQL type carries
`#[GraphQLType('LLM')]`, which the runtime reads when loading objects by ID and
registering module types. Renamed enum cases keep their old names as
deprecated constants.

### Module runtimes

Runtime function dispatch is unaffected. SDKs register functions with their
native names, and the engine dispatches by `OriginalName`, so nothing on the
dispatch path depends on names round-tripping.

Calls a runtime builds itself, through a module's own interfaces, do depend on
schema names: the runtime has to know what the engine named the interface type,
its functions and their arguments. The Python and TypeScript runtimes no longer
predict those names. Before invoking a function of a module that declares
interfaces, they ask the engine with `Query.formatIdentifiers`, in batches, and
use the results for selections, argument keys and `node(id:)` type names (the
TypeScript runtime namespaces each interface the way the engine does,
comparing words). The gate is
the field itself: a runtime's session is served at its module's view, which
has `formatIdentifiers` exactly when the engine names the module by the new
rules (see [The gate](#the-gate)), so older modules keep the conversion they
always used. Go module codegen mirrors `Namer` instead (see
[Which namer applies](#which-namer-applies)).

### Formatting names in codegen

SDK codegen doesn't format names itself: it asks the engine. It collects the
schema's names (every type, field, argument, input field and enum value name,
except introspection names with a `__` prefix and names with no letters or
digits) and calls `formatIdentifiers(names, casing, acronyms, version)` once
for each casing and acronym style it uses, which gives it a map from each name
to its SDK identifier. `version` is the generated schema's `__schemaVersion`,
so the engine parses the names with the dictionary of the schema's engine
version (a module's `engineVersion`), not the codegen client's. A run makes one
to three such calls (Go asks for `PASCAL` and `CAMEL` with `UPPERCASE`;
TypeScript also for `PASCAL` with `CAPITALIZED`), concurrently.

That works because codegen nearly always has an engine connection, or reads
its input from a step that has one:

- Go and TypeScript codegen (`cmd/codegen`) run in an engine session.
- The PHP and Java generators run the introspection query against the engine.
- Python, Rust and Elixir read the JSON an online step writes
  (`codegen introspect`).
- Only .NET's Roslyn source generator runs without one, and its input also
  comes from an online step.

In Go, `cmd/codegen/introspection` has the helpers: `Schema.Names` collects the
names, `FormatNames` makes the calls (in batches, if the list gets large), and
`Schema.LoadFormattedNames` / `Schema.FormattedName` keep the maps on the
schema for the generators. `cmd/codegen` connects to the engine whenever it
generates from introspection JSON that has `Query.formatIdentifiers`.

**Gate:** the schema being generated. If it has no `Query.formatIdentifiers`
(schema views before `v1.0.0-0`, the identifier API's gate, views of modules
that keep the legacy naming rules, or older engines), codegen formats nothing
through the engine and keeps its legacy converter, so older modules and
clients regenerate byte-identical code. A name missing from a map (one that
isn't among the schema's names) also gets the legacy converter.

**Offline generators** get the names from the online step, as a sidecar file
next to the introspection JSON, which stays unchanged:

```shell
codegen introspect -o schema.json \
  --names-out names.json --names SNAKE:UPPERCASE,PASCAL:CAPITALIZED
```

```json
{
  "PASCAL:CAPITALIZED": {"httpClient": "HttpClient", "withGPU": "WithGpu"},
  "SNAKE:UPPERCASE": {"httpClient": "http_client", "withGPU": "with_gpu"}
}
```

Each key is a `CASING:ACRONYMS` pair of `Casing` and `AcronymStyle` values, and
maps every schema name to its formatted form. A schema without
`Query.formatIdentifiers` gets `{}`. A generator uses its legacy converter for a
format or name the file doesn't have, so the same gate applies.

**Module runtimes** hand their codegen the same sidecar. The Python, PHP,
Elixir and Java runtimes run codegen in an exec without an engine session, so
the runtime formats the module schema's names itself and mounts the file for
codegen (Python asks for `SNAKE:UPPERCASE`; PHP and Elixir for the formats
their generators use; Java for `CAMEL:CAPITALIZED`, `PASCAL:CAPITALIZED` and
`SCREAMING_SNAKE:UPPERCASE`, in `sdk/java/runtime/names.go`). The runtime
modules declare `engineVersion` v1.0.0, so their own sessions have
`formatIdentifiers`; they call it with `version` set to the module schema's
`__schemaVersion`, so the names are parsed with the dictionary of the module's
version, not the runtime's.

**Performance.** Formatting is cheap: parsing and formatting the core schema's
961 names takes about 0.7ms. Through the API, it first cost about 780ms per
call in the engine (measured with wcprof on a dev engine, one call with the
961 names): about 1ms formatting, 210ms publishing the result to the cache,
250ms giving each of the 961 returned strings a cache entry of its own, and
the rest copying the call (with its 961-element argument) once per element and
other per-element overhead. Two changes cut a call to about 11ms, most of it
the fixed cost of a request (a call formatting one name takes about 8ms):

- `formatIdentifiers` isn't cached. A hit wouldn't help much anyway: each
  call of a codegen run asks for a different format.
- dagql renders the leaf elements of a list of plain values straight from the
  list, without a result per element (`valueEnumerable` in `dagql`). This
  helps every field returning a list of scalars.

Codegen also sends its one to three calls concurrently, so formatting costs a
codegen run roughly one round trip.

An earlier revision of this design had the engine write each name's words into
the schema JSON (`__identifiers`) and every SDK format them, on the premise
that codegen runs offline. That premise was mostly false, and it put a copy of
the formatting rules into each SDK. The engine no longer writes the words, and
every in-repo SDK has moved to `formatIdentifiers` (or the sidecar) in place of
its word formatter.

## Versioning and Compatibility

### The gate

The algorithm and the dictionary are selected by engine version through the
existing view mechanism (`AfterVersion` / `BeforeVersion` in `core/util.go`):

- A module's own names are parsed with the version its `engineVersion` selects.
  Existing modules keep strcase behavior until they bump. A module entrypoint
  (manifest v2), which declares no `engineVersion`, keeps strcase behavior
  (see [Module entrypoints](#module-entrypoints)).
- A client's codegen uses the version the client connects with.
- A dictionary addition lands with an engine version and applies only to
  modules and clients at or above it.

`naming.DictionaryFor(engineVersion)` maps a version to the dictionary it
selects: no version means the latest dictionary, and a version below
`naming.FirstVersion` (`v1.0.0`), or an invalid one, gets `naming.Initial`.
Each dictionary release adds an entry to the list in
`engine/naming/version.go`.

Module normalization is gated separately, at `IdentifierNamingVersion`
(`core/gqlformat.go`): a module keeps the strcase rules unless the
`engineVersion` it declares is at least that version, comparing full versions,
prerelease included (`ModuleSource.LegacyNaming`). It's `v1.0.0-beta.17`, the
first engine to ship these rules; it moves to whichever release they first
ship in. A view gate wouldn't do: API views are base-version granular, so
every v1.0.0 prerelease (`v1.0.0-beta.1` to `v1.0.0-beta.16`, and the `v1.0.0-0`
the repo's own modules declare) shares the `v1.0.0` view, and a view gate
would rename every module released against those betas.

Such a module still gets the v1.0.0 API, so its rules travel apart from its
view: `LegacyNamingView` marks a view with `+legacy-naming` build metadata.
Semver precedence ignores build metadata, so every view filter treats the
marked view like the plain one (same fields, same arguments), while
`NamerForView` gives it the legacy rules. `ModuleSource.NamingView` is the
module's view, marked when the module keeps the legacy rules and its view
would otherwise have the new ones (views before `v1.0.0` need no mark, so
older modules keep sharing results with clients at their view). The engine
serves everything about the module at that view:

- its runtime's client (`engine/server/session.go`), so the typedef
  constructors it calls record the marked view and name by the legacy rules.
  The mark is part of the call, so it keys the cache, and a typedef loaded
  again from its ID is named the same way. The rest of the module's calls are
  keyed apart from other clients' too, which costs some sharing between them
  and clients at the plain view;
- its dependencies' schema (`loadDependencyModules`), which its codegen
  generates from;
- the in-engine Dang runtime's direct selects (`dangshared.WithModuleNaming`).

The marked view hides `Query.formatIdentifiers`, the one gate SDK codegen, Go
module codegen and the Python and TypeScript runtimes check, so a
legacy-naming module's codegen keeps its legacy converters (byte-identical to
what it generated before) and its runtime keeps predicting the names the
engine gives it. `__schemaVersion` reports the version without the mark: SDK
codegen parses it for other gates, not always as semver (PHP's
`version_compare` would order `v1.0.0+legacy-naming` before
`v1.0.0-beta.10`).

`NamerForView` gives the new rules to every unmarked view from `v1.0.0`, which
only matters for clients that aren't modules: the typedefs they construct
themselves.

Known gaps:

- The TypeScript SDK's module introspection JSON emitter
  (`sdk/typescript/src/module/introspector/introspection_json.ts`) still
  predicts schema names with ports of strcase. It isn't wired in and runs
  without an engine connection; whoever wires it should resolve names through
  the engine like the runtimes do (see [Module runtimes](#module-runtimes)).
- CLI naming follows the CLI's engine version, not each module's (see
  [CLI](#cli)).
- Go module codegen (`module_naming.go`) still names the module's own types,
  fields and arguments with the `engine/naming` it was built with, using
  `DictionaryFor` of the module's schema version. It compares words, not just
  formatted strings (namespacing checks whether a type's words start with the
  module's), which `formatIdentifiers` can't answer. A codegen binary older
  than the engine could therefore miss dictionary additions once there is more
  than one dictionary. Fetching the dictionary of the module's version from
  the engine into `naming.NewDictionary` would close that gap (today
  `Query.namingDictionary` follows the caller's version; it could take a
  `version` like `formatIdentifiers`).

### Module entrypoints

Manifest v2 modules (#14038, `future/module-manifest-v2/spec.md`) have a
`dagger-module.toml` with a `name` and an `[entrypoint]` table, and no
`engineVersion`. The engine evaluates the entrypoint, a Dang program, in
process (`core/sdk/dang/v2/entrypoint.go`): its `types` builds the module's
typedefs with the typedef constructors (`typeDef.withObject(...)`,
`function(...)`, `withArg(...)`), and its `call` receives each call by
original names (`receiverType`, `fnName`, the keys of `fnArgs`). An SDK
generates the entrypoint at `dagger generate` (`sdk/entrypoint/types.dang`,
`main.dang` and `build.dang`, with `types.dang` a literal list of typedef
constructor calls, e.g. python-sdk's `sdk/tests/mod/golden/types.dang`), or
the module points at a shared remote entrypoint that describes its types when
it loads.

**Why load-time naming is wrong for them.** An entrypoint module gets the
engine's own version as its `EngineVersion` (`initFromModConfig` in
`core/schema/modulesource.go`), and so the engine's API. If that also chose
its naming rules, the schema names of its declarations would be decided
afresh by whichever engine loads it:

- Shipping this design would rename existing entrypoint modules. An SDK that
  registers `getUrl` (Python's own `snake_to_camel` of `get_url`) would see it
  become `getURL`, and break the code, generated clients and CLI flags of
  everyone calling it.
- Every dictionary addition would rename them again on upgrade, which is what
  versioning the dictionary (`DictionaryFor`) exists to prevent: a module's
  names follow the version it declares, and an entrypoint module declares
  none.

Dispatch doesn't care, since calls carry original names, but every name a
caller sees does: constructor fields, namespaced types, functions, arguments,
CLI paths.

**The gate.** Until an entrypoint can pin its naming, entrypoint modules keep
the legacy (strcase) rules everywhere the engine names their declarations, so
their schema is byte-identical to what engines before this design gave them:

- `ModuleSource.LegacyNaming` is true for an entrypoint source, and
  `ModuleSource.Namer` (so `Module.Namer`) is the legacy `Namer`: namespacing,
  the constructor name, validation, CLI paths.
- The entrypoint's client is served at the engine's API view marked for legacy
  naming, like a module that declares an older v1.0.0 prerelease (see
  [The gate](#the-gate)): the typedef constructors the entrypoint calls name
  by the legacy rules, and the module's codegen sees no
  `Query.formatIdentifiers`.

Re-normalizing an entrypoint module's typedefs from their original names
when the module is assembled would also work, but needs an internal rename for
every typedef kind and member, and leaves the cached typedefs named the other
way; marking the view keeps the decision in the call, like every other naming
decision.

**Opting in: pinned naming.** Entrypoint modules should get the new rules,
but decided at `dagger generate`, never at load, so that upgrading the engine
never renames anything. Two options:

- **(A) Final names in the entrypoint.** The generator calls
  `formatIdentifiers` at `dagger generate` and writes final schema names into
  `types.dang`, and the engine takes an entrypoint's names as declared. It
  needs a signal that the names are final (an older entrypoint's names must
  still be normalized), a way to declare both a name and its original name
  (calls carry original names), and pinned names for what the engine derives
  rather than declares: namespaced type names, the constructor field, CLI
  paths.
- **(B) A declared naming version.** The generated entrypoint declares the
  engine version it was generated with, as an optional key in the
  `[entrypoint]` table (say `namingVersion = "v1.0.0"`) or a field on the
  `ModuleEntrypoint` interface, and the engine names the module with
  `NamerForEngineVersion` of that version, as it does with a `dagger.json`
  `engineVersion`. A newer engine keeps naming by the declared version's
  dictionary; the next `dagger generate` re-pins, the way an `engineVersion`
  bump does today.

We recommend (B), as a key in the `[entrypoint]` table. It reuses the
mechanism end to end: `ModuleSource.NamingView` returns the declared
version's view instead of the marked one, and the session already serves the
module's client at it. The rules stay in the engine, the
generator only writes a version string, and the derived names come along for
free. A manifest key also works for a shared remote entrypoint, which a field
generated into `types.dang` doesn't: a remote entrypoint has no generate step
and describes its types at load, so it can only pin through the module's own
manifest, and stays on the legacy rules until the module declares a version.

Either option needs a signal in the manifest v2 contract (a naming version,
or a marker that names are final), which #14038's authors own; this design
only ships the gate.

### What changes for module authors (on bump)

A module that bumps its `engineVersion` to `IdentifierNamingVersion` or later
(`latest`, or anything from `v1.0.0-beta.17` on) gets the new rules. Module
type, field and argument names that contain runs of capitals or dictionary
terms get their real casing back:

- `MyModHttpclient` → `MyModHTTPClient`
- `Llmmessage` → `LLMMessage`, and references to core `LLM*` / `JSON*` types
  resolve to the core type instead of a namespaced copy.
- CLI flags: `E2ETest` → `--e2e-test` (was `--e-2-e-test`, which is still
  accepted).
- Python arguments: `com_url` → `comURL` (was `comUrl`).

Names that were already canonical don't change.

### Core schema

Core names are written by hand in Go and are *not* run through the
normalizer. Measured against `docs/docs-graphql/schema.graphqls` with the
initial dictionary, before the API above added its own (canonical) names:

- All 126 type names are already canonical.
- 696 of 703 field/argument names are already canonical. The 7 exceptions:
  `asSdkName`, `callId`, `filterUri`, `parentShas`, `pushUrl`, `shortSha`,
  `withoutUri`.
- One enum still uses legacy PascalCase values with no `SCREAMING_SNAKE`
  aliases: `FunctionCachePolicy` (`Default`, `PerSession`, `Never`).
  `ImageLayerCompression` and `ImageMediaTypes` already have aliases.

`TestCoreSchemaCanonical` in `engine/naming` asserts that every core type name
and every field/argument name outside an allowlist of the 7 is canonical, and
fails when an allowlisted name becomes canonical; legacy enum values are only
logged. Whether to rename the 8, with deprecated aliases, before 1.0 is an
[open question](#open-questions).

### What changes for SDK users

Measured on the core client each SDK regenerates in this change, against the
code it generated before:

- **Go**: 7 methods (and their option structs) — `ParentShas` → `ParentSHAs`,
  `Sdks` → `SDKs`, `Sha` → `SHA`, `ShortSha` → `ShortSHA`, `SshfsVolume` →
  `SSHFSVolume`, `VcsGeneratedPaths` → `VCSGeneratedPaths`, `VcsIgnoredPaths`
  → `VCSIgnoredPaths` — plus enum constants that strcase had capitalized:
  `NetworkProtocolTcp` → `NetworkProtocolTCP` (and `Udp`),
  `RegistryProtocolHttp` → `RegistryProtocolHTTP` (and `Https`),
  `ImageMediaTypesOcimediaTypes` → `ImageMediaTypesOCIMediaTypes` (and `Oci`),
  `ImageLayerCompressionEstarGz` → `ImageLayerCompressionEStarGZ`. The old
  names stay as deprecated wrappers and aliases. Parameter renames (`pushUrl`
  → `pushURL`) don't affect Go callers.
- **TypeScript**: 5 methods — `Artifacts.filterUri` → `filterURI`,
  `Artifacts.withoutUri` → `withoutURI`, `GitCommit.parentShas` →
  `parentSHAs`, `GitCommit.shortSha` → `shortSHA`, `LLMContentBlock.callId` →
  `callID` — plus 5 Opts types (`ArtifactUriOpts` → `ArtifactURIOpts`,
  `ClientHttpOpts` → `ClientHTTPOpts`, `ClientSshfsVolumeOpts` →
  `ClientSSHFSVolumeOpts`, `WorkspaceWithSdkOpts` → `WorkspaceWithSDKOpts`,
  `WorkspaceWithoutSdkOpts` → `WorkspaceWithoutSDKOpts`) and 2 opts keys
  (`pushUrl` → `pushURL`, `asSdkName` → `asSDKName`), all kept as deprecated
  aliases. The positional parameter `LLM.withToolResult(callId)` becomes
  `callID`. No type or enum member changes.
- **Python**: 2 methods — `prerequisite_sh_as` → `prerequisite_shas`,
  `experimental_with_all_gp_us` → `experimental_with_all_gpus` — kept as
  deprecated aliases. Enum members are `SCREAMING_SNAKE`:
  `FunctionCachePolicy.DEFAULT`, `NEVER` and `PER_SESSION`,
  `ImageLayerCompression.ESTARGZ`, `GZIP`, `UNCOMPRESSED` and `ZSTD`,
  `ImageMediaTypes.DOCKER` and `OCI`, which used to be aliases of the legacy
  members, are now the canonical members, and the legacy names their
  deprecated aliases. Values don't change for core enums. A dependency
  module's enum member now holds its schema value (`READY`) rather than the
  module's underlying value (`"ready"`).
- **Rust**: the same 2 methods as Python, kept as deprecated aliases. No type
  changes (`JsonValue` stays).
- **Elixir**: 1 function — `prerequisite_sh_as` → `prerequisite_shas` — kept
  as a deprecated alias.
- **PHP**: members are now `CAMEL`/`CAPITALIZED`. 19 methods change only in
  case (`withGPU` → `withGpu`), which PHP method names ignore: `traceURL`,
  `withGPU`, `experimentalWithGPU`, `experimentalWithAllGPUs`, `asJSON`,
  `withVCSGeneratedPaths`, `withVCSIgnoredPaths`, `prerequisiteSHAs`,
  `previousSHA`, `commitSHA`, `withMCPServer`, `introspectionSchemaJSON` (on
  `Module` and `ModuleSource`), `withSDK` (on `ModuleSource` and `Workspace`),
  `withoutSDK`, `clientSchemaIntrospectionJSON`, `htmlURL` and `htmlRepoURL`.
  Legacy enum cases become `SCREAMING_SNAKE`
  (`FunctionCachePolicy::PER_SESSION`, `ImageLayerCompression::E_STAR_GZ`,
  `ImageMediaTypes::OCI_MEDIA_TYPES`, ...) with the old names as deprecated
  constants. 3 parameters — `insecureSkipTLSVerify` → `insecureSkipTlsVerify`
  on `from` and `publish`, `expectedRemoteSHA` → `expectedRemoteSha` on
  `GitRef::push` — break named-argument callers. Classes with acronyms are
  renamed in letter case (`LLM` → `Llm`, `LLMContent` → `LlmContent` and the
  other `LLM*` types, `SDKConfig` → `SdkConfig`, `HTTPState` → `HttpState`,
  `WorkspaceSDK` → `WorkspaceSdk`); their old spellings still load, as
  deprecated.
- **Java**: the same 19 case-only method renames as PHP (`asJSON` → `asJson`,
  `withGPU` → `withGpu`, ...), plus optional-argument setters
  (`withInsecureSkipTLSVerify` → `withInsecureSkipTlsVerify`), all kept as
  deprecated forwarders. Parameter names don't affect Java callers. 17 classes
  are renamed with no alias: `ID` → `Id`, `JSON` → `Json`, `JSONValue` →
  `JsonValue`, the 11 `LLM*` types (`LLM` → `Llm`, `LLMContentBlock` →
  `LlmContentBlock`, ...), `SDKConfig` → `SdkConfig`, `HTTPState` → `HttpState`
  and `WorkspaceSDK` → `WorkspaceSdk`. Enum constants named like legacy values
  become `SCREAMING_SNAKE` (`FunctionCachePolicy.PER_SESSION`,
  `ImageLayerCompression.E_STAR_GZ`, `ImageMediaTypes.OCI_MEDIA_TYPES`, ...),
  with the old names as deprecated static aliases; they still serialize to the
  schema's values.
- **.NET**: 3 methods — `WithVcsgeneratedPaths` → `WithVcsGeneratedPaths`
  (and `WithVcsignoredPaths`), `WithMcpserver` → `WithMcpServer` — kept as
  obsolete forwarders; 3 parameters — `insecureSkipTLSVerify` →
  `insecureSkipTlsVerify` on `From` and `PublishAsync`, `expectedRemoteSHA` →
  `expectedRemoteSha` on `GitRef.Push` — which break named-argument callers.
  16 types are renamed with no alias: `JSON` → `Json`, `JSONValue` →
  `JsonValue`, the 11 `LLM*` types, `SDKConfig` → `SdkConfig`, `HTTPState` →
  `HttpState` and `WorkspaceSDK` → `WorkspaceSdk` (`ID` was already `Id`). Enum
  members become PascalCase (`NetworkProtocol.Tcp`, `TypeDefKind.StringKind`,
  `FunctionCachePolicy.PerSession`, ...), with the old names as `[Obsolete]`
  aliases; they still serialize to the schema's values.

Renamed identifiers stay as deprecated aliases wherever the language allows
one. The hard breaks are:

- the PHP and .NET parameter renames above: neither language can alias a
  parameter name;
- the Java and .NET type renames: a Java class can't sit next to its
  case-only alias on case-insensitive file systems, and .NET types can't be
  aliased across assemblies;
- Python enum members' `name`: an alias resolves to the new member, so code
  that reads `.name` (or lists an enum's members) sees `PER_SESSION`, not
  `PerSession`;
- Python dependency module enums' `value`, now the schema value instead of the
  module's underlying value.

## Guarantees

Checked against the [test vectors](#test-vectors) and the full core schema by
the `engine/naming` tests.

1. **Round trip.** If every acronym and term in `x` is in the dictionary, then
   `parse(format(parse(x), C, S))` has the same words as `parse(x)`, for every
   casing `C` except `FLAT` and both styles `S`. Acronyms the dictionary doesn't
   know survive only formats that keep capitals (`PASCAL`, and `CAMEL` after the
   first word, with `UPPERCASE`).
2. **Idempotence.** Formatting the result again changes nothing:
   `format(parse(format(parse(x), C, UPPERCASE)), C, UPPERCASE)` equals
   `format(parse(x), C, UPPERCASE)` for `PASCAL`, `CAMEL`, `SNAKE` and
   `SCREAMING_SNAKE`.
3. **Canonical names come back unchanged.** This is what makes
   `withFinalTypeName` unnecessary, and why every core type name passes
   through unchanged.

One caveat applies to the last two: a lowercase word is never split, but the
same word in all caps is, wherever dictionary entries cover it completely. So
an ordinary word spelled like a run of entries changes under `SCREAMING_SNAKE`
(without the `SSHFS` entry, `sshfsVolume` → `SSHFS_VOLUME` → `SSH_FS_VOLUME`).
Such a word is an acronym the dictionary doesn't know, and the remedy is to add
it; `SSHFS` is in the initial dictionary for exactly this reason, and the core
schema has no other case.

## Known Limitations

| case | result | remedy |
| --- | --- | --- |
| Unknown acronym through a lowercase form | `httpx_client` → `HttpxClient` | add it to the dictionary |
| Unknown adjacent acronyms | `HTTPXAPIClient` → `HTTPXAPI` · `client` | add the unknown one |
| Unknown mixed-case brand | `PostgreSQL` (not an initial entry) → `postgre_sql` | add the term |
| A term followed by an acronym in one run of capitals | `gRPCAPI` → `g` · `RPC` · `API`; `iOSSDK` → `i` · `OSSDK` | write `gRPC_API`, or add the combination |
| An unknown acronym spelled like a run of entries, in lowercase | `sshfs` stays a word, but `SSHFS` splits into `SSH` · `FS` | add the acronym (`SSHFS` is an initial entry) |
| Dictionary overrides author casing | `Sha256Sum` → `SHA256Sum`; `HttpClient` → `HTTPClient` | intended |
| A dictionary entry splits a lowercase word at a digit | `md5sum` → `md5_sum` / `MD5Sum` (because `MD5` is an entry) | intended; `base64`, `int32` stay whole |
| Digit-led words not in the dictionary | `Get4KStream` → `get4_k_stream` | add the term (`4K`) |
| `FLAT` | drops boundaries | output only |

## Test Vectors

These start the shared test-case file, `engine/naming/testdata/vectors.json`,
which the `engine/naming` tests check the package against. SDKs don't need a
copy: they get formatted names from the engine.
`^` marks `ACRONYM`, `*` marks `TERM`, `+` a suffix.

| input | words | `PASCAL` | `CAMEL` | `SNAKE` | `PASCAL` / `CAPITALIZED` |
| --- | --- | --- | --- | --- | --- |
| `HTTPClient` | ^HTTP · client | `HTTPClient` | `httpClient` | `http_client` | `HttpClient` |
| `httpClient` | ^HTTP · client | `HTTPClient` | `httpClient` | `http_client` | `HttpClient` |
| `HttpClient` | ^HTTP · client | `HTTPClient` | `httpClient` | `http_client` | `HttpClient` |
| `http_client` | ^HTTP · client | `HTTPClient` | `httpClient` | `http_client` | `HttpClient` |
| `HTTP_CLIENT` | ^HTTP · client | `HTTPClient` | `httpClient` | `http_client` | `HttpClient` |
| `HTTPAPIClient` | ^HTTP · ^API · client | `HTTPAPIClient` | `httpAPIClient` | `http_api_client` | `HttpApiClient` |
| `LLMContentBlock` | ^LLM · content · block | `LLMContentBlock` | `llmContentBlock` | `llm_content_block` | `LlmContentBlock` |
| `E2ETest` | ^E2E · test | `E2ETest` | `e2eTest` | `e2e_test` | `E2eTest` |
| `e2e_test` | ^E2E · test | `E2ETest` | `e2eTest` | `e2e_test` | `E2eTest` |
| `prerequisiteSHAs` | prerequisite · ^SHA+s | `PrerequisiteSHAs` | `prerequisiteSHAs` | `prerequisite_shas` | `PrerequisiteShas` |
| `experimentalWithAllGPUs` | experimental · with · all · ^GPU+s | `ExperimentalWithAllGPUs` | `experimentalWithAllGPUs` | `experimental_with_all_gpus` | `ExperimentalWithAllGpus` |
| `userIds` | user · ^ID+s | `UserIDs` | `userIDs` | `user_ids` | `UserIds` |
| `listPRs` | list · ^PR+s | `ListPRs` | `listPRs` | `list_prs` | `ListPrs` |
| `IPv6Address` | *IPv6 · address | `IPv6Address` | `ipv6Address` | `ipv6_address` | `Ipv6Address` |
| `ipv6_address` | *IPv6 · address | `IPv6Address` | `ipv6Address` | `ipv6_address` | `Ipv6Address` |
| `OAuth2Token` | *OAuth+2 · token | `OAuth2Token` | `oauth2Token` | `oauth2_token` | `Oauth2Token` |
| `oauth2_token` | *OAuth+2 · token | `OAuth2Token` | `oauth2Token` | `oauth2_token` | `Oauth2Token` |
| `GitHubRepo` | *GitHub · repo | `GitHubRepo` | `githubRepo` | `github_repo` | `GitHubRepo` |
| `github_repo` | *GitHub · repo | `GitHubRepo` | `githubRepo` | `github_repo` | `GitHubRepo` |
| `iOSApp` | *iOS · app | `IOSApp` | `iosApp` | `ios_app` | `IosApp` |
| `macOSBuild` | *macOS · build | `MacOSBuild` | `macosBuild` | `macos_build` | `MacOSBuild` |
| `gRPCServer` | *gRPC · server | `GRPCServer` | `grpcServer` | `grpc_server` | `GrpcServer` |
| `Get3DModel` | get · ^3D · model | `Get3DModel` | `get3DModel` | `get_3d_model` | `Get3DModel` |
| `get3d_model` | get · ^3D · model | `Get3DModel` | `get3DModel` | `get_3d_model` | `Get3DModel` |
| `Base64URLEncode` | base64 · ^URL · encode | `Base64URLEncode` | `base64URLEncode` | `base64_url_encode` | `Base64UrlEncode` |
| `Win32API` | win32 · ^API | `Win32API` | `win32API` | `win32_api` | `Win32Api` |
| `HTTP2Server` | ^HTTP+2 · server | `HTTP2Server` | `http2Server` | `http2_server` | `Http2Server` |
| `Sha256Sum` | ^SHA+256 · sum | `SHA256Sum` | `sha256Sum` | `sha256_sum` | `Sha256Sum` |
| `md5sum` | ^MD5 · sum | `MD5Sum` | `md5Sum` | `md5_sum` | `Md5Sum` |
| `S3Bucket` | s3 · bucket | `S3Bucket` | `s3Bucket` | `s3_bucket` | `S3Bucket` |
| `V1Beta1Foo` | v1 · beta1 · foo | `V1Beta1Foo` | `v1Beta1Foo` | `v1_beta1_foo` | `V1Beta1Foo` |
| `ModuleAOverlay` | module · a · overlay | `ModuleAOverlay` | `moduleAOverlay` | `module_a_overlay` | `ModuleAOverlay` |
| `IOStream` | ^IO · stream | `IOStream` | `ioStream` | `io_stream` | `IoStream` |
| `Capital` | capital | `Capital` | `capital` | `capital` | `Capital` |
| `Identity` | identity | `Identity` | `identity` | `identity` | `Identity` |
| `HTTPXClient` | ^HTTPX · client | `HTTPXClient` | `httpxClient` | `httpx_client` | `HttpxClient` |
| `httpx_client` | httpx · client | `HttpxClient` | `httpxClient` | `httpx_client` | `HttpxClient` |
| `E2EAPI` | ^E2E · ^API | `E2EAPI` | `e2eAPI` | `e2e_api` | `E2eApi` |
| `JSONAPI` | ^JSON · ^API | `JSONAPI` | `jsonAPI` | `json_api` | `JsonApi` |
| `HTMLURL` | ^HTML · ^URL | `HTMLURL` | `htmlURL` | `html_url` | `HtmlUrl` |
| `HTTPX_CLIENT` | httpx · client | `HttpxClient` | `httpxClient` | `httpx_client` | `HttpxClient` |
| `Café` | error: non-ASCII | | | | |

## Open Questions

1. **.NET's two-letter rule** (`IOStream` but `HtmlTag`; `Id` and `Ok` are
   exceptions). It only affects C#, F# and VB, and only two-letter acronyms
   other than `ID`. In the core schema that is `GZ`, in an enum value the .NET
   SDK writes as is, so it changes no C# name. Proposal: leave
   it out. .NET uses `CAPITALIZED`, and `Identifier.words` carries what's needed
   if the .NET SDK ever wants the exact form. A third `AcronymStyle` value can be
   added later without breaking anyone.
2. **Renaming the 8 non-canonical core names** before 1.0, with deprecated
   aliases, or keeping them on an allowlist.
3. **Offline codegen.** Some SDK codegen paths were thought to run without an
   engine session. Resolved: nearly all codegen has an engine connection, or
   reads its input from a step that has one, so codegen asks the engine to
   format names with `formatIdentifiers`, and SDKs need neither a parser nor a
   formatter. The one generator that runs truly offline, .NET's source
   generator, reads names the online `codegen introspect` step formatted (see
   [Formatting names in codegen](#formatting-names-in-codegen)). An earlier
   answer, writing each name's words into the schema JSON for SDKs to format,
   is gone.
4. **Splitting all-caps input** (`E2EAPI`) by full dictionary cover, as for
   caps pieces. Resolved: each chunk of all-caps input splits only if
   dictionary entries (and digit runs) cover it completely; lowercase input
   never splits, because the same rule on lowercase words risks false
   positives. Without it, `PASCAL` names made only of acronyms (`JSONAPI`,
   `HTMLURL`) wouldn't come back unchanged.
5. **Java and PHP class names.** Strict "when in Rome" would write them
   `CAPITALIZED` (`JsonValue`, `Llm` in Java; `JsonValue` throughout in PHP).
   A case-only class rename can't keep a deprecated alias, so this would be a
   breaking change, best left for a major version. Resolved for Java (and
   .NET): taken before 1.0, as hard breaks (see
   [What changes for SDK users](#what-changes-for-sdk-users)).
6. **TypeScript's convention.** The ecosystem writes acronyms both ways. The
   generator uses `UPPERCASE` members and `CAPITALIZED` enum members today;
   confirm with the TypeScript maintainers.
7. **Input object field names.** Rust and Elixir (and possibly Python) had
   existing bugs where an input object field is sent by its SDK name rather
   than its schema name. The new codegen maps renamed fields back to their
   wire names without changing behavior; the underlying bugs are a separate
   fix.

## SDK Extraction

The SDKs are moving out of this repository, to
`github.com/dagger/{go,python,typescript,php,java,dang}-sdk`
(`core/sdk/sdkmeta/sdks.json`). The in-repo `cmd/codegen` and `sdk/*` stay the
reference implementation of this design, which the external repositories
vendor or port from:

- typescript-sdk vendors `sdk/typescript` at a tag (see its
  `library/VENDOR.md`);
- go-sdk has its own fork of the Go generator, `cmd/dagger-go-sdk-codegen`;
- python-sdk has its own codegen, `sdk/codegen`.

Porting engine-formatted names (`formatIdentifiers` or the names sidecar, with
the legacy fallback) to each of them is a follow-up per repository. Until an
SDK has it, it keeps its legacy converter, which the gate makes byte-identical
for schemas without `formatIdentifiers` anyway. Those SDKs also generate the
manifest v2 entrypoints, where pinned naming would land (see
[Module entrypoints](#module-entrypoints)).

## Implementation Plan

1. Write the `engine/naming` package: parser, formatter, dictionary, and the
   test-vector file as table tests, with property tests for the guarantees.
2. Add the core API (`identifier`, `formatIdentifiers`, `namingDictionary`) and
   regenerate the SDKs.
3. Switch engine normalization (`core/gqlformat.go`, typedef constructors,
   `namespaceObject`, CLI kebab names) to the package behind an engine-version
   view. Keep strcase behavior for older modules.
4. Keep `strcase.ConfigureAcronym` in `core/llm.go` / `core/json.go` and the
   `withFinalTypeName` re-normalization workaround for the legacy path only;
   delete them once modules below the gate are no longer supported.
5. Add the core-schema canonical-name test with the allowlist.
6. Move SDK codegen to names the engine formats (`formatIdentifiers`, or the
   `codegen introspect --names-out` sidecar for offline generators), one SDK at
   a time: Go and TypeScript, then Python, Rust and Elixir (the visible plural
   bugs), PHP, Java and .NET, removing the word formatters they gained when
   the engine wrote words into the schema JSON.
7. Resolve the names module runtimes build themselves (Python and TypeScript
   interface calls) through `formatIdentifiers`.
8. Bump the Python, PHP, Elixir and Java runtimes' `engineVersion` to v1, and
   format their codegen sidecars with `formatIdentifiers` and `version`.
9. Keep manifest v2 entrypoint modules on the legacy rules, then let a
   generated entrypoint pin its naming version (see
   [Module entrypoints](#module-entrypoints)).

Steps 1–8 are done for the in-repo SDKs, except step 4's deletion, which waits
on the legacy path. Step 9's gate is done; pinning waits on a manifest v2
signal. Porting to the extracted SDKs is a follow-up (see
[SDK Extraction](#sdk-extraction)).
