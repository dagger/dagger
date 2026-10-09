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
  the rest of the schema.
- `Casing` and `AcronymStyle` can gain values later without breaking anyone.
- Like every object in the schema, `IdentifierWord` and `NamingTerm` also
  implement `Node`.
- The API is `@experimental` and visible to clients at engine version
  `v1.0.0-0` and above, so prereleases see it.

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
  call's view). A module runtime connects at its `engineVersion`, so its
  typedefs are named by its version. Public constructors pass the view on to
  the internal `__*` selects they make; the in-engine Dang runtime stamps its
  module's view on its naming selectors (`core/sdk/dang/shared/naming.go`).
- **Module-level naming** (`namespaceObject`, type and constructor lookups,
  validation) uses `Module.Namer()`, chosen by the module source's
  `engineVersion`. Module tree CLI paths (checks, generators, artifacts) use
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
  `module_naming.go` mirrors `Namer`, gated by the introspection JSON's
  `__schemaVersion`.

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

Codegen formats names from the words the engine writes into the schema JSON
(see [Schema JSON words](#schema-json-words)); Go codegen, written in Go, uses
`engine/naming` directly. Each SDK keeps its old converter only as the fallback
for schemas without words, so older schemas generate byte-identical code.

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
| Python | schema name | `SNAKE` | `SNAKE` | schema value |
| Elixir | `PASCAL`/`UPPERCASE` | `SNAKE` | `SNAKE` | schema-value atoms (`SNAKE` functions) |
| Rust | `PASCAL`/`CAPITALIZED` | `SNAKE` | `SNAKE` | `PASCAL`/`CAPITALIZED` |
| PHP | existing converter (`JsonValue`) | `CAMEL`/`CAPITALIZED` | `CAMEL`/`CAPITALIZED` | `SCREAMING_SNAKE` |
| .NET | schema name (`ID` written `Id`) | `PASCAL`/`CAPITALIZED` | `CAMEL`/`CAPITALIZED` | schema value |
| Java | schema name (`PASCAL`/`UPPERCASE`) | `CAMEL`/`CAPITALIZED` | `CAMEL`/`CAPITALIZED` | schema value |

Only SDK identifiers change. Selected fields, argument names, input object
keys, enum values and GraphQL type names go over the wire as the schema has
them. Where an SDK serializes an input object field by its own name, a
renamed field is mapped back to its wire name (`#[serde(rename)]` in Rust,
`@JsonbProperty` in Java; Elixir keeps the legacy struct keys).

Type names stay as the schema has them, or as the SDK's existing converter
writes them, in SDKs whose runtime looks types up by name (TypeScript, Python,
Java, PHP) or where a case-only rename can't be aliased (Java and PHP class
files, .NET types across assemblies). Enum values stay put where they are
serialized by member name (Python, Java, .NET).

TypeScript's own convention is ambiguous (`XMLHttpRequest`, `innerHTML`).
Members follow the schema's `UPPERCASE` (`filterURI`, `callID`), and enum
members keep the `PASCAL`/`CAPITALIZED` form the generator always wrote
(`Tcp`, `Oci`).

.NET follows the Framework Design Guidelines for members: methods and input
object properties are PascalCase and parameters camelCase, with acronyms
written like words (`WithMcpServer`, `insecureSkipTlsVerify`). The guidelines'
two-letter exception (`IOStream`) is left out: the only two-letter acronym in
the core schema besides `ID` (which the guidelines write `Id`) is `GZ`, in an
enum value, so it changes no C# name. Classes keep the schema's names. Generated
code writes GraphQL type names as literals, so renaming classes would be safe on
the wire, but it would break every user of `LLM`, `JSON`, `JSONValue` and the
like with no alias: enums, input structs and return types can't be aliased
across assemblies. Enum members keep the schema's values, because
`JsonStringEnumConverter` and argument serialization write a member's C# name.
Renamed methods and properties keep their old names as `[Obsolete]` forwarders
(default interface methods on interfaces). Renamed parameters can't be
aliased, since C# can't overload on parameter names.

Java follows Google Java Style for members (`asJson`, `withGpu`), keeping the
old spellings as deprecated forwarders. Its classes and enum constants keep the
schema's names: the runtime uses a class's simple name as its GraphQL type and
serializes enum constants by name, and a class renamed only in case can't keep
a deprecated alias next to it on case-insensitive file systems.

PHP follows the Symfony and Laravel convention of writing acronyms like words
in members (`filterUri`, `withGpu`). PHP method names are case-insensitive, so
those changes are cosmetic; parameter names matter for named arguments. Classes
keep today's converter (`ID`/`JSON` written `Id`/`Json`, the rest as the schema
has them), since a case-only class rename only breaks PSR-4 autoloading of the
old spelling. A class whose name differs from its GraphQL type carries
`#[GraphQLType('JSONValue')]`, which the runtime reads when loading objects by
ID and registering module types. Renamed enum cases keep their old names as
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
the field itself: a runtime's session is served at its module's engine
version, and `formatIdentifiers` exists exactly from the version the naming
rules start at, so older modules keep the conversion they always used. Go
module codegen mirrors `Namer` instead (see
[Which namer applies](#which-namer-applies)).

### Schema JSON words

Most SDK codegen runs offline from the introspection JSON the engine hands it
(the .NET source generator, the Java Maven plugin, PHP, Python), so it can't
call `formatIdentifiers`. Instead the engine writes each name's words into
that JSON, next to `__schema` and `__schemaVersion`:

```json
"__identifiers": {
  "httpClient": [
    {"kind": "ACRONYM", "text": "HTTP", "suffix": "", "capitalized": "Http"},
    {"kind": "WORD", "text": "client", "suffix": "", "capitalized": "Client"}
  ]
}
```

- **Keys:** every type, field, argument, input field and enum value name in
  the JSON, except introspection names (`__` prefix) and names with no letters
  or digits. Parsing is context-free, so each distinct name appears once.
- **Words:** `kind`, `text` and `suffix` as in `IdentifierWord`; `capitalized`
  is the word's `CAPITALIZED`-style form without the suffix (the entry's
  `capitalized` for dictionary words, else the first letter capitalized and
  the rest lowercase).

An SDK formats the words itself, which needs no dictionary:

- `SNAKE`, `KEBAB`, `FLAT`, and the first word of `CAMEL`: lowercase
  `text + suffix`. `SCREAMING_SNAKE`: uppercase `text + suffix`.
- Capitalized form (`PASCAL`, the other words of `CAMEL`): with `CAPITALIZED`,
  or for a `WORD`, `capitalized`; otherwise `text` with its first letter
  uppercased. Then the suffix.

`engine/naming/testdata/vectors.json` lists inputs with their words and every
format, for testing these formatters. Each SDK's formatter tests run against
it.

The JSON comes from `__schemaJSONFile` (so module and client introspection
JSON), `Schema.merge` (which adds the words of the names it merges in), and
`cmd/introspect`. Codegen that runs the GraphQL introspection query itself
(`codegen introspect`, and the PHP and Java client generators) fetches the
words from `Query.identifier`, in batches, since the query can't carry them.

**Gate:** `__identifiers` appears only for schema views at `v1.0.0-0` and
above, the identifier API's gate, parsed with the caller's dictionary. When it
is absent, SDKs keep their current converters, so older modules regenerate
unchanged.

## Versioning and Compatibility

### The gate

The algorithm and the dictionary are selected by engine version through the
existing view mechanism (`AfterVersion` / `BeforeVersion` in `core/util.go`):

- A module's own names are parsed with the version its `engineVersion` selects.
  Existing modules keep strcase behavior until they bump.
- A client's codegen uses the version the client connects with.
- A dictionary addition lands with an engine version and applies only to
  modules and clients at or above it.

`naming.DictionaryFor(engineVersion)` maps a version to the dictionary it
selects: no version means the latest dictionary, and a version below
`naming.FirstVersion` (`v1.0.0`), or an invalid one, gets `naming.Initial`.
Each dictionary release adds an entry to the list in
`engine/naming/version.go`. Module normalization is gated separately, at
`IdentifierNamingVersion` (`v1.0.0-0`, so prereleases opt in): below it, the
`Namer` uses strcase.

Known gaps:

- The TypeScript SDK's module introspection JSON emitter
  (`sdk/typescript/src/module/introspector/introspection_json.ts`) still
  predicts schema names with ports of strcase. It isn't wired in and runs
  without an engine connection; whoever wires it should resolve names through
  the engine like the runtimes do (see [Module runtimes](#module-runtimes)).
- CLI naming follows the CLI's engine version, not each module's (see
  [CLI](#cli)).

### What changes for module authors (on bump)

Module type, field and argument names that contain runs of capitals or
dictionary terms get their real casing back:

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
  deprecated aliases.
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
  `GitRef::push` — break named-argument callers. No class changes.
- **Java**: the same 19 case-only method renames as PHP (`asJSON` → `asJson`,
  `withGPU` → `withGpu`, ...), plus optional-argument setters
  (`withInsecureSkipTLSVerify` → `withInsecureSkipTlsVerify`), all kept as
  deprecated forwarders. Parameter names don't affect Java callers. No class
  or enum constant changes.
- **.NET**: 3 methods — `WithVcsgeneratedPaths` → `WithVcsGeneratedPaths`
  (and `WithVcsignoredPaths`), `WithMcpserver` → `WithMcpServer` — kept as
  obsolete forwarders; 3 parameters — `insecureSkipTLSVerify` →
  `insecureSkipTlsVerify` on `From` and `PublishAsync`, `expectedRemoteSHA` →
  `expectedRemoteSha` on `GitRef.Push` — which break named-argument callers.
  No type, property or enum member changes.

Renamed identifiers stay as deprecated aliases wherever the language allows
one. The only hard breaks are the PHP and .NET parameter renames above: neither
language can alias a parameter name.

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

These start the shared test-case file that every implementation (the engine
package, plus any SDK that keeps a local copy for offline use) must pass.
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
3. **Offline codegen.** Some SDK codegen paths run without an engine session.
   Resolved: the engine ships each name's words in the introspection JSON it
   already hands to codegen (see [Schema JSON words](#schema-json-words)), and
   SDKs only format them, which needs no dictionary, so SDKs don't need a
   local parser.
4. **Splitting all-caps input** (`E2EAPI`) by full dictionary cover, as for
   caps pieces. Resolved: each chunk of all-caps input splits only if
   dictionary entries (and digit runs) cover it completely; lowercase input
   never splits, because the same rule on lowercase words risks false
   positives. Without it, `PASCAL` names made only of acronyms (`JSONAPI`,
   `HTMLURL`) wouldn't come back unchanged.
5. **Java and PHP class names.** Strict "when in Rome" would write them
   `CAPITALIZED` (`JsonValue`, `Llm` in Java; `JsonValue` throughout in PHP).
   A case-only class rename can't keep a deprecated alias, so this would be a
   breaking change, best left for a major version.
6. **TypeScript's convention.** The ecosystem writes acronyms both ways. The
   generator uses `UPPERCASE` members and `CAPITALIZED` enum members today;
   confirm with the TypeScript maintainers.
7. **Input object field names.** Rust and Elixir (and possibly Python) had
   existing bugs where an input object field is sent by its SDK name rather
   than its schema name. The new codegen maps renamed fields back to their
   wire names without changing behavior; the underlying bugs are a separate
   fix.

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
6. Write each name's words into the schema JSON, and move SDK codegen to
   format from them one SDK at a time: Python, Rust and Elixir (the visible
   plural bugs), Go (golint list), TypeScript, PHP, Java and .NET.
7. Resolve the names module runtimes build themselves (Python and TypeScript
   interface calls) through `formatIdentifiers`.

Steps 1–7 are done; step 4's deletion waits on the legacy path.
