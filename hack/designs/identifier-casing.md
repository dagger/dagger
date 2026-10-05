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
2. **Caps piece cover:** a whole caps piece written as a sequence of dictionary
   entries and digit runs. `HTTPAPI` → `HTTP`+`API`; `HTTP2` → `HTTP`+`2`;
   `APIs` → `API`+`s` (only the last entry can take the piece's plural `s`). This
   is the only place the dictionary looks *inside* a segment, because a run of
   capitals has no internal boundary information. If the cover isn't complete
   (`HTTPX`), this candidate doesn't apply.
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
segments are never split internally. The only place inside a word where a
dictionary entry can apply is a letter/digit cut (`get3d` → `get` + `3D`).

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
- Acronyms found in the core schema: `GPU VCS SHA MCP OCI`
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
  `v1.0.0` and above.

## Engine Integration

All engine-side case conversion goes through one Go package (e.g.
`engine/naming`), which the core API also wraps:

| today | replaced by |
| --- | --- |
| `gqlObjectName` (`strcase.ToCamel`) | `PASCAL` / `UPPERCASE` |
| `gqlFieldName`, `gqlArgName` (`strcase.ToLowerCamel`) | `CAMEL` / `UPPERCASE` |
| `gqlEnumMemberName` (`strcase.ToScreamingSnake`) | `SCREAMING_SNAKE` |
| `namespaceObject` prefix check (`'A' <= rest[0] <= 'Z'`) | compare word lists: the object's words start with the module's words |
| `strcase.ToKebab` for CLI flags and addresses (`core/modtree.go`, `core/artifacts.go`, `core/schema/address.go`, `engine/server/session_workspaces.go`) | `KEBAB` |
| `strcase.ConfigureAcronym` in `core/llm.go` | dictionary |
| `withFinalTypeName` / `__withName` re-application in `core/typedef_results.go` | not needed: canonical names are fixed points |

CLI flags change too: `E2ETest` becomes `--e2e-test` instead of strcase's
`--e-2-e-test`.

## SDK Integration

Codegen calls `formatIdentifiers` (or the Go package directly, for codegen
written in Go) and deletes its own conversion code. What each SDK should ask
for:

| SDK | types | methods / fields | args | enum values |
| --- | --- | --- | --- | --- |
| GraphQL schema | `PASCAL`/`UPPERCASE` | `CAMEL`/`UPPERCASE` | `CAMEL`/`UPPERCASE` | `SCREAMING_SNAKE` |
| Go | `PASCAL`/`UPPERCASE` | `PASCAL`/`UPPERCASE` | `CAMEL`/`UPPERCASE` | `PASCAL`/`UPPERCASE` |
| TypeScript | `PASCAL`/`UPPERCASE` | `CAMEL`/`UPPERCASE` | `CAMEL`/`UPPERCASE` | `SCREAMING_SNAKE` |
| Python | `PASCAL`/`UPPERCASE` | `SNAKE` | `SNAKE` | `SCREAMING_SNAKE` |
| Elixir | `PASCAL`/`UPPERCASE` | `SNAKE` | `SNAKE` | `SNAKE` atoms |
| Rust | `PASCAL`/`CAPITALIZED` | `SNAKE` | `SNAKE` | `PASCAL`/`CAPITALIZED` |
| PHP | `PASCAL`/`CAPITALIZED` | `CAMEL`/`UPPERCASE` | `CAMEL`/`UPPERCASE` | `SCREAMING_SNAKE` |
| .NET | `PASCAL`/`CAPITALIZED` | `PASCAL`/`CAPITALIZED` | `CAMEL`/`CAPITALIZED` | `PASCAL`/`CAPITALIZED` |
| Java | `PASCAL`/`CAPITALIZED` | `CAMEL`/`CAPITALIZED` | `CAMEL`/`CAPITALIZED` | `SCREAMING_SNAKE` |

The type and method/field columns for Go, TypeScript, Python, Elixir, Rust and
PHP match what those SDKs generate today. The argument and enum columns, and
the .NET and Java rows, are proposals to confirm with each SDK's maintainers.
Today .NET passes type names through unchanged (`LLMMessageRole`) and
title-cases members.

Runtime function dispatch is unaffected. SDKs register functions with their
native names, and the engine dispatches by `OriginalName`, so nothing on the
dispatch path depends on names round-tripping.

## Versioning and Compatibility

### The gate

The algorithm and the dictionary are selected by engine version through the
existing view mechanism (`AfterVersion` / `BeforeVersion` in `core/util.go`):

- A module's own names are parsed with the version its `engineVersion` selects.
  Existing modules keep strcase behavior until they bump.
- A client's codegen uses the version the client connects with.
- A dictionary addition lands with an engine version and applies only to
  modules and clients at or above it.

### What changes for module authors (on bump)

Module type, field and argument names that contain runs of capitals or
dictionary terms get their real casing back:

- `MyModHttpclient` → `MyModHTTPClient`
- `Llmmessage` → `LLMMessage`, and references to core `LLM*` / `JSON*` types
  resolve to the core type instead of a namespaced copy.

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

Proposal: add an engine test asserting that every core name is canonical,
with an allowlist for these 8. Decide separately whether to rename them, with
deprecated aliases, before 1.0.

### What changes for SDK users

Measured on the core schema, comparing today's codegen with the proposed
casing:

- **Go**: 6 names — `ParentShas` → `ParentSHAs`, `Sdks` → `SDKs`, `Sha` →
  `SHA`, `ShortSha` → `ShortSHA`, `VcsGeneratedPaths` → `VCSGeneratedPaths`,
  `VcsIgnoredPaths` → `VCSIgnoredPaths`.
- **Python**: 2 names — `prerequisite_sh_as` → `prerequisite_shas`,
  `experimental_with_all_gp_us` → `experimental_with_all_gpus`.
- **TypeScript, PHP methods**: none (they use schema names as is).
- **Rust, Elixir, .NET**: not measured in full. Rust has both of Python's
  plural bugs and Elixir has `prerequisite_sh_as`; .NET also fixes
  `InsecureSkipTlsverify` → `InsecureSkipTLSVerify` (or
  `InsecureSkipTlsVerify` in `CAPITALIZED`).

Where cheap, SDKs keep the old names as deprecated aliases for one release.

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

One exception applies to all three: a name made only of acronyms and terms
formats in `PASCAL`/`UPPERCASE` with no lowercase letter (`htmlURL` →
`HTMLURL`). That output parses caseless, and lowercase is never split, so it
comes back as one word (`htmlurl`, see [Known Limitations](#known-limitations)).
The only such core name is the `htmlURL` field, which the schema uses in
`CAMEL`, where it round-trips.

## Known Limitations

| case | result | remedy |
| --- | --- | --- |
| Unknown acronym through a lowercase form | `httpx_client` → `HttpxClient` | add it to the dictionary |
| Unknown adjacent acronyms | `HTTPXAPIClient` → `HTTPXAPI` · `client` | add the unknown one |
| Unknown mixed-case brand | `PostgreSQL` (not an initial entry) → `postgre_sql` | add the term |
| Adjacent acronyms in all-caps input without separators | `E2EAPI` → `e2eapi` (caseless; lowercase is never split) | write `E2E_API` |
| A name made only of acronyms, in `PASCAL`/`UPPERCASE` | `htmlURL` → `HTMLURL` → `htmlurl` | format it in another casing, or with `CAPITALIZED` (`HtmlUrl`) |
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
| `E2EAPI` | e2eapi | `E2eapi` | `e2eapi` | `e2eapi` | `E2eapi` |
| `Café` | error: non-ASCII | | | | |

## Open Questions

1. **.NET's two-letter rule** (`IOStream` but `HtmlTag`; `Id` and `Ok` are
   exceptions). It only affects C#, F# and VB, and only two-letter acronyms
   other than `ID`, which don't appear in the core schema today. Proposal: leave
   it out. .NET uses `CAPITALIZED`, and `Identifier.words` carries what's needed
   if the .NET SDK ever wants the exact form. A third `AcronymStyle` value can be
   added later without breaking anyone.
2. **Renaming the 8 non-canonical core names** before 1.0, with deprecated
   aliases, or keeping them on an allowlist.
3. **Offline codegen.** Some SDK codegen paths may run without an engine
   session. If so, should they use the test-vector file plus a local
   implementation, or should the engine ship formatted names in the
   introspection JSON it already hands to codegen?
4. **Splitting all-caps input** (`E2EAPI`) by full dictionary cover, as for
   caps pieces. Rejected for now: the same rule on lowercase words risks
   false positives, and all-caps input almost always has separators.

## Implementation Plan

1. Write the `engine/naming` package: parser, formatter, dictionary, and the
   test-vector file as table tests, with property tests for the guarantees.
2. Add the core API (`identifier`, `formatIdentifiers`, `namingDictionary`) and
   regenerate the SDKs.
3. Switch engine normalization (`core/gqlformat.go`, typedef constructors,
   `namespaceObject`, CLI kebab names) to the package behind an engine-version
   view. Keep strcase behavior for older modules.
4. Delete `strcase.ConfigureAcronym` in `core/llm.go` and the
   `withFinalTypeName` re-normalization workaround once older modules are
   handled by the legacy path.
5. Add the core-schema canonical-name test with the allowlist.
6. Move SDK codegen to `formatIdentifiers` one SDK at a time, starting with
   Python, Rust and Elixir (the visible plural bugs), then Go (golint list), then
   TypeScript, PHP and .NET.
