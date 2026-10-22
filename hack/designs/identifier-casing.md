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
and enum values, CLI flag names, and the Go generator's identifiers. That
replaces strcase in the engine for modules from the cut on.

From `v1.0.0-beta.22` (`engine.IdentifierCasingVersion`) on, the core schema
follows the same convention: its few names that didn't are renamed at the
cut, with no aliases.

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
| Suffix | Trailing digits and/or a plural `s`, in that order, attached to an acronym or term: `SHA`+`s`, `OAuth`+`2`, `HTTP`+`2s`. The plural ends the word. An ordinary word keeps its digits in its text. |
| Casing | The joining convention: `PASCAL`, `CAMEL`, `SNAKE`, `SCREAMING_SNAKE`, `KEBAB`, `FLAT`. |
| Acronym style | How acronyms and terms are written where a word starts with a capital: `UPPERCASE` (`HTTPClient`) or `CAPITALIZED` (`HttpClient`). |
| Canonical name | `format(parse(x), casing, style)` for the casing and acronym style the schema uses at that position: `PASCAL`/`UPPERCASE` for types (`HTTPClient`), `CAMEL`/`CAPITALIZED` for fields and arguments (`httpClient`, `baseUrl`), `SCREAMING_SNAKE` for enum values (see [Schema casing convention](#schema-casing-convention)). |
| Dictionary | The engine's versioned list of acronyms and terms. |

## Parsing

Parsing turns a string into words. Steps 1–3 split the input using its own
structure, step 4 picks words using the dictionary with the heuristic as
fallback, and steps 5–6 tidy up.

### 1. Validate and chunk

- Reject non-ASCII input, input with no letters or digits, and input longer
  than 256 bytes (`naming.MaxLength`). The limit bounds the work a name can
  cost the engine, since `formatIdentifiers` parses whatever a client sends;
  real names are far shorter.
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
a boundary before position `k` when any of these holds:

- **(a)** `k` is a capital, and the nearest earlier character that isn't a
  digit is lowercase: `httpClient` → `http|Client`, `Get3DModel` →
  `Get3|DModel`, `Base64URLEncode` → `Base64|URLEncode`.
- **(b)** `k` is a capital followed by a lowercase letter, and preceded by a
  capital or digit. The last capital of a run starts the next word:
  `HTTPClient` → `HTTP|Client`, `E2ETest` → `E2E|Test`, `DModel` → `D|Model`.
  - **Plural exception:** no boundary if that lowercase letter is an `s` that
    ends the chunk or is followed by a capital or digit. `prerequisiteSHAs` →
    `prerequisite|SHAs`, `IDsFoo` → `IDs|Foo`, `listPRs` → `list|PRs`.
  - **Letter-digit exception:** no boundary if that lowercase letter is
    followed by a digit and the capital ends a run of two or more; rule (c)
    splits after the run instead.
- **(c)** `k` is a lowercase letter followed by a digit, after a run of two or
  more capitals. A lone letter with digits is a word of its own, typically a
  version: `APIv1` → `API|v1`, `UUIDv7` → `UUID|v7`, `IOx86` → `IO|x86`. A
  single capital doesn't count as a run, so capitalized forms stay whole:
  `Vp9Codec`, `Ec2Instance`, `Ipv6Address`. Dictionary matches cross the new
  boundary like any other, so `IPv6` (`IP|v6`) is still one term.

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
   (case-insensitively), or the spelling plus a suffix: `s`, or digits and
   `s`.
   - `I`+`Pv`+`6` → `IPv6`; `Git`+`Hub` → `GitHub`; `3`+`D` → `3D`; `e`+`2`+`e`
     → `E2E`; `Ids` → `ID`+`s`; `HTTP`+`2`+`s` → `HTTP`+`2s`.
   - A plural ends the word, so it never matches right before a digit
     segment: in `APIS3` (`APIS`, `3`), `APIS` isn't `API`+`s`.
2. **Caps piece cover:** a whole caps piece, or a whole chunk of all-caps input
   (step 2), written as a sequence of dictionary entries, digit runs, and
   letter-digit words. `HTTPAPI` → `HTTP`+`API`; `HTTP2` → `HTTP`+`2`; `APIs`
   → `API`+`s` and `APIHTTP2s` → `API`+`HTTP`+`2s` (only the last entry can
   take the piece's plural `s`, after its digits); `APIV1` → `API`+`V1`,
   `APIS3` → `API`+`S3`. This is the only place the dictionary looks *inside*
   a segment, because a run of capitals has no internal boundary information.
   If the cover isn't complete (`HTTPX`), this candidate doesn't apply.
   - A **letter-digit word** is one letter and the digits after it: `V1`,
     `S3`, `R2`. It becomes an ordinary word (`v1`), and its letter counts as
     not covered by the dictionary, so it only splits a run that entries
     otherwise cover: `UUIDV7` → `UUID`·`v7`, but `M3U8` stays whole, since
     neither `M` nor `U` is an entry.
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
`E2E`). Digits never follow a plural: an `s` that ends a word's suffix ends the
word (`HTTP`+`2s`), and digits after an `s` belong to a letter-digit word of
their own (`ID`·`s2`).

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

### Schema casing convention

Schema names follow the prevailing GraphQL convention, which is also what
agents are trained on: acronyms in capitals in type names, written like words
in field and argument names (`avatarUrl`, `databaseId`):

| position | casing / acronyms | examples |
| --- | --- | --- |
| types (objects, interfaces, enums, scalars, inputs) | `PASCAL`/`UPPERCASE` | `JSONValue`, `HTTPClient` |
| fields, functions, arguments (and collection members) | `CAMEL`/`CAPITALIZED` | `asJson`, `baseUrl`, `filterUri`, `httpClient` |
| enum values | `SCREAMING_SNAKE` | `HTTP_CLIENT`, `E2E_TEST` |
| CLI commands and flags | `KEBAB` | `--base-url` |

Terms keep their entry's `capitalized` form in fields (`githubRepo`,
`fromGitHubIpv6`, `myK8sCluster`). The core schema mixed both acronym styles
in fields (`traceURL` but `pushUrl`, `commitSHA` but `shortSha`) and in enum
values (`PerSession` but `GZIP`); v1.0 is the time to fix that. From
`v1.0.0-beta.22` (`engine.IdentifierCasingVersion`) on, the convention applies
both to module names (see [The gate](#the-gate)) and to the core schema, whose
names that didn't follow it are renamed at the cut (see
[Core schema](#core-schema)).

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
covers `HTTP2` and `HTTP2s`, `SHA` covers `SHA256`. Nor are they needed for
letter-digit words like versions and product names: `APIv1`, `APIS3` and
`APIR2` split without `V1`, `S3` or `R2` entries.

### Initial contents

- golint's `commonInitialisms` (what the Go SDK uses today): `ACL API ASCII CPU
  CSS DNS EOF GUID HTML HTTP HTTPS ID IP JSON LHS QPS RAM RHS RPC SLA SMTP SQL
  SSH TCP TLS TTL UDP UI UID UUID URI URL UTF8 VM XML XMPP XSRF XSS`
- The Go SDK's Dagger additions: `FS SDK LLM`
- Acronyms found in the core schema: `GPU VCS SHA MCP OCI SSHFS`
- Common in modules: `E2E CLI TUI IO PR EC2 MD5`
- Terms: `GitHub GitLab OAuth IPv4 IPv6 iOS macOS gRPC GraphQL 3D 2D K8s K3s`.
  Their `capitalized` forms are `Oauth Ipv4 Ipv6 Ios MacOS Grpc`; the brands
  `GitHub GitLab GraphQL`, `3D 2D` and the numeronyms `K8s K3s` keep their
  spelling. `K8s` and `K3s` are product names, not acronyms, so `K8STools`,
  `K8sTools` and `k8s-tools` all parse as *K8s · tools. They were added before
  any engine named modules with the dictionary (only the experimental
  `formatIdentifiers` had shipped, in v1.0.0-beta.20, and no SDK used it), so
  they're in `Initial` rather than a later dictionary. Other numeronyms
  (`k0s`, `i18n`, `l10n`, `a11y`) are left to the heuristic: lowercase or
  capitalized they parse as one word, and their all-caps spellings (`I18N`)
  are as common as the lowercase ones, so no spelling is clearly canonical.

### Process

New entries are requested in an issue or PR and ship with the next engine
release. Each addition renames names for anyone who already has them (a Python
`httpx_client` goes from `HttpxClient` to `HTTPXClient`), so additions follow
the engine-version gate in
[Versioning and Compatibility](#versioning-and-compatibility). A released
dictionary is never edited; `TestInitialDictionaryFrozen` pins
`naming.Initial`, entry by entry, and fails with a pointer to the release list
if it changes.

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
  Errors on non-ASCII input, input with no letters or digits, or input over
  256 bytes.
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

  """Trailing digits and/or a plural "s", in that order: SHA+"s", OAuth+"2", HTTP+"2s"."""
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
  per name, and codegen formats thousands of names.
- `identifier` and `namingDictionary` follow the caller's engine version, like
  the rest of the schema. So does `formatIdentifiers`, unless given a
  `version`: a caller generating code for a schema passes its
  `__schemaVersion`, so the names are parsed with the dictionary of that
  schema's engine version, whatever version the connection is served at.
- `formatIdentifiers` isn't cached: it's pure and fast, and caching a result
  with a large list argument costs more than formatting it.
- `Casing` and `AcronymStyle` can gain values later without breaking anyone.
- Like every object in the schema, `IdentifierWord` and `NamingTerm` also
  implement `Node`.
- The API is `@experimental` and visible to clients on every v1 view
  (`v1.0.0-0` and above), including the views whose modules keep the strcase
  names. Which rules a module's names follow is decided by the engine version
  it declares (see [The gate](#the-gate)), not by the API's presence; the
  `version` argument of `formatIdentifiers` selects the dictionary.

## Engine Integration

All engine-side case conversion goes through `engine/naming`, which the core
API also wraps. In `core`, a `Namer` (`core/gqlformat.go`) holds either a
naming dictionary or nothing; a nil dictionary is the legacy path, which keeps
the old strcase code byte for byte:

| legacy (strcase) | `Namer` with a dictionary |
| --- | --- |
| `gqlObjectName` (`strcase.ToCamel`) | `ObjectName`: `PASCAL` / `UPPERCASE` |
| `gqlFieldName`, `gqlArgName` (`strcase.ToLowerCamel`) | `FieldName`, `ArgName`: `CAMEL` / `CAPITALIZED` |
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
  rules. A runtime client that declares its own version (`DAGGER_CLIENT_VERSION`
  or the client metadata header) is served that version's core API instead,
  but its typedef constructors still select at the module's view: the engine
  stamps it on their selections (`core.ContextWithNamingView`), so declared
  client versions move only the core API, and the module's declarations are
  always named by the module's version. Public constructors pass the view on
  to the internal `__*` selects they make; the in-engine Dang runtime stamps
  its module's view on its naming selectors
  (`core/sdk/dang/shared/naming.go`).
- **Module-level naming** (`namespaceObject`, type and constructor lookups,
  validation) uses `Module.Namer()`, chosen by the module source's
  `engineVersion` (`ModuleSource.Namer`; a module entrypoint's version comes
  from its `dang.toml`, see [Module entrypoints](#module-entrypoints)). Module
  tree CLI paths (checks, generators, artifacts)
  use the namer of the module that declared the node, so a legacy dependency
  keeps its old paths inside a new workspace.
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
- **SDK runtime arguments.** SDK interface arguments keep their names across
  the cut: `introspectionJson` is spelled alike by strcase and by the new
  field convention.
- **Go codegen** (`cmd/codegen`) can't import `core`, but imports
  `engine/naming`: for a schema whose `__schemaVersion` is at or past the cut,
  it formats Go identifiers with that version's dictionary (see
  [SDK Integration](#sdk-integration)). It doesn't mirror `Namer` for a Go
  module's own names: see the Known gaps under [The gate](#the-gate).

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

The principle is **"when in Rome"**: each SDK writes identifiers by its own
language's convention, not one house style. Go writes initialisms in capitals
(`CommitSHA`, `ParentSHAs`), and the snake-case SDKs need only the word
boundaries (`prerequisite_shas`). SDKs only choose identifiers: selected
fields, argument names, input object keys, enum values and GraphQL type names
go over the wire as the schema has them.

The core renames (see [Core schema](#core-schema)) reach the SDKs as wire-name
changes; the in-repo clients (Go, Python, TypeScript, PHP, Rust, Elixir) are
regenerated from a schema past the cut. Only the Go generator changes how it
forms identifiers; the others keep their own converters:

| SDK | identifiers from a schema past the cut | examples |
| --- | --- | --- |
| Go | types, methods and option structs by `engine/naming`, `PASCAL`/`UPPERCASE`, with the schema version's dictionary; parameters keep the schema name; enum constants as before | `CommitSHA`, `WithGPU`, `AsJSON`, `ShortSHA`, `FunctionCachePolicyNever` |
| TypeScript | members are the schema's field and argument names; types and enum members by its own converter | `commitSha()`, `withGpu()`, `WorkspaceWithSdkOpts`, `FunctionCachePolicy.Never` |
| PHP | methods named after the schema's fields | `commitSha()`, `withGpu()`, `asJson()` |
| Python, Rust, Elixir | their own snake-case converters | `prerequisite_shas`, `experimental_with_all_gpus` |

**Go** (`goNamer`, `cmd/codegen/generator/go/templates/naming.go`) formats a
name by `engine/naming` when the generated schema's `__schemaVersion` is at or
past `engine.IdentifierCasingVersion` (a copy of the constant, kept in sync by
a test, since codegen doesn't import `engine`). Earlier schemas keep the
golint-style initialisms byte for byte, so code generated for pre-cut modules
doesn't change. Since `commitSha` and `commitSHA` parse to the same words,
the renamed core fields keep their Go identifiers (`CommitSHA`, `WithGPU`,
`AsJSON`); a few that golint's list spelled otherwise change (`Sha` → `SHA`,
`ShortSha` → `ShortSHA`, `ParentShas` → `ParentSHAs`, `Sdks` → `SDKs`,
`SshfsVolume` → `SSHFSVolume`, `VcsGeneratedPaths` → `VCSGeneratedPaths`).
Identifiers derived from dependency module names are reformatted the same way
(`Github` → `GitHub`, `Cli` → `CLI`, `CreatePr` → `CreatePR`, `Ids` → `IDs`).
There are no deprecated aliases for the old Go identifiers, core or
dependency: a Go module that bumps its `engineVersion` past the cut updates
its callers with the bump.

**TypeScript**'s own convention is ambiguous (`XMLHttpRequest`, `innerHTML`).
Its members follow the schema's field names, so they follow the renames
(`commitSha()`, `withMcpServer()`). Enum members keep the
`PASCAL`/`CAPITALIZED` form the generator always wrote, with the schema's
values (`FunctionCachePolicy.Never` is `"NEVER"`).

**Enum members** follow the schema in every SDK: past the cut, the members of
the dropped spellings are gone (Go's `ImageLayerCompressionEstarGz`,
TypeScript's `EStarGz`, Python's `Gzip`), and Python's
`FunctionCachePolicy` members are `DEFAULT`, `PER_SESSION` and `NEVER`. The
Python module runtime registers a function's cache policy with whichever
member its generated client has.

No SDK keeps deprecated aliases for renamed identifiers. The Java and .NET
generators aren't changed.

### Module runtimes

Runtime function dispatch is unaffected. SDKs register functions with their
native names, and the engine dispatches by `OriginalName`, so nothing on the
dispatch path depends on names round-tripping.

Calls a runtime builds itself, through a module's own interfaces, do depend on
schema names: the runtime has to know what the engine named the interface type,
its functions and their arguments. The in-repo Python and TypeScript runtimes
build those names themselves (Python with its own `to_camel_case`, TypeScript
from the names the module declares), not through `formatIdentifiers`, so for a
module past the cut a name the new rules spell differently can miss. Resolving
them through the engine isn't part of this stack. The identifier API is on
every v1 view, so its presence says nothing about which rules apply.

## Versioning and Compatibility

### The gate

The algorithm and the dictionary are selected by engine version through the
existing view mechanism (`AfterVersion` / `BeforeVersion` in `core/util.go`):

- A module's own names are parsed with the version its `engineVersion` selects.
  Modules below `IdentifierNamingVersion` (`v1.0.0-beta.22`) keep strcase
  behavior until they bump. A module entrypoint (manifest v2) has no
  `engineVersion` in its manifest: its version is the Dagger version its
  `dang.toml` declares, `v1.0.0-beta.20` (the shared pre-cut view) without
  one, and it's named by that version's rules like any other module (see
  [Module entrypoints](#module-entrypoints)).
- The core schema's renamed names exist by the caller's view: the old
  spellings before the cut only, the new ones from the cut on only (see
  [Core schema](#core-schema)).
- A client's codegen uses the version the client connects with.
- A dictionary addition lands with an engine version and applies only to
  modules and clients at or above it.

`naming.DictionaryFor(engineVersion)` maps a version to the dictionary it
selects: no version means the latest dictionary, and a version below
`naming.FirstVersion` (`v1.0.0`), or an invalid one, gets `naming.Initial`.
Each dictionary release adds an entry to the list in
`engine/naming/version.go`.

Module normalization is gated on the API view, at `IdentifierNamingVersion`
(`engine.IdentifierCasingVersion`, `v1.0.0-beta.22`, `core/gqlformat.go`):
`NamerForView` gives the strcase rules to views before it and engine/naming to
the rest (`IdentifierNamingView`), and a module is named by the rules of its
view (`ModuleSource.NamingView`, `engine.ModuleAPIViewVersion` of its
`engineVersion`, capped at the engine's own view). Views keep their
prerelease from `v1.0.0-beta.22`
(`engine.PrereleaseViewsVersion`) on, and every earlier v1.0.0 prerelease
(`v1.0.0-0`, `v1.0.0-beta.1` to `v1.0.0-beta.21`) shares the `v1.0.0-beta.21`
view, so modules released against those betas keep the strcase names they
shipped with, byte for byte.
A prerelease engine serves a module declaring a view past its own (a later
beta, or exactly `v1.0.0`) at its own view, and names it by that view's
rules, so no engine names a module by rules it doesn't have. The Go
generator gates on the same cut, by the generated schema's `__schemaVersion`,
which for a module is that capped view
(see [SDK Integration](#sdk-integration)). Nothing gates on
`Query.formatIdentifiers` being in the schema: it is served on every v1 view
(from `v1.0.0-0`).

There are no aliases for the old spellings: a module's schema has exactly the
names of its own rules, and bumping `engineVersion` past the cut is an opt-in
rename, like any other API change a bump brings. The
typedef constructors that normalize names are view-scoped
(`View(AllVersion)`), so their calls record the caller's view and modules on
either side of the cut never share a cached, wrongly named typedef.

`NamerForView` gives the new rules to every view from the cut, which also
names the typedefs that clients that aren't modules construct themselves.

Known gaps:

- The TypeScript SDK's module introspection JSON emitter
  (`sdk/typescript/src/module/introspector/introspection_json.ts`) still
  predicts schema names with ports of strcase. It isn't wired in and runs
  without an engine connection; whoever wires it should resolve names through
  the engine.
- CLI naming follows the CLI's engine version, not each module's (see
  [CLI](#cli)). For a legacy module whose name has digits or terms (`k8s`),
  `dagger call` spells its collection selectors by the latest rules
  (`--k8s-cluster`) while the engine's artifact dimensions, and so
  `dagger list` and `check`, spell them by the module's (`--k-8-s-cluster`).
- The in-repo Go module codegen (`cmd/codegen`) still names a module's own
  types, fields and arguments in its self-binding schema by strcase, so a Go
  module at the new rules that references a core type starting with an
  acronym (`JSONValue`, `LLMMessage`) doesn't build. Mirroring `core.Namer`
  there (a `moduleNamer`) moves to the Go SDK repository
  (github.com/dagger/go-sdk), with the rest of the Go codegen work.

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

**Naming.** An entrypoint module is named by whatever engine version its
`ModuleSource` ends up with, with no special path. That version is the Dagger
version the `dang.toml` governing its entrypoint declares (`[imports.Dagger]
dagger = "vX.Y.Z"`, read by `resolveEntrypointVersions` in
`core/schema/modulesource.go`), or `v1.0.0-beta.20` when it declares none (see
`future/module-manifest-v2/spec.md`, "Versions"). Its client is served at that
version's view, capped at the engine's own like any module's
(`ModuleSource.NamingView`), so the typedef constructors its entrypoint calls
name by those rules, `Module.Namer` names its namespaced types, constructor
and CLI paths by them, and its codegen gates on that same version. Dispatch
doesn't care, since calls carry original names.

An entrypoint that declares no version gets the shared pre-cut view
(`v1.0.0-beta.21`, the view of every v1.0.0 prerelease before the cut): the
old core names, which every generated entrypoint spells today
(`FunctionCachePolicy.Never`, `PerSession`, `Default`), and the strcase
rules. Shipping the cut changes nothing for it. One that declares the cut or
later gets the renamed core API and has its type acronyms renamed: a module
`tiny`'s `HttpClient` type becomes `TinyHTTPClient` (was `TinyHttpClient`).
Fields keep acronyms written like words, so an SDK that registers `getUrl`
(Python's own `snake_to_camel` of `get_url`) keeps `getUrl`; names with
digits or terms still change (`e2ETest` → `e2eTest`). There are no aliases
for the old spellings.

**Pinned naming.** A later dictionary addition would rename an entrypoint
module again on engine upgrade, which is what versioning the dictionary
(`DictionaryFor`) exists to prevent: a module's names follow the version it
declares, and an entrypoint module declared none until its `dang.toml` could
(see the outcome below). Its names should be decided at `dagger generate`,
never at load. Two options:

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
version's view, and the session already serves the module's client at it. The
rules stay in the engine, the generator only writes a version string, and the
derived names come along for free. A manifest key also works for a shared
remote entrypoint, which a field generated into `types.dang` doesn't: a remote
entrypoint has no generate step and describes its types at load, so it can
only pin through the module's own manifest, and follows the engine's version
until the module declares one.

Either option needs a signal in the manifest v2 contract (a naming version,
or a marker that names are final), which #14038's authors own. Nothing needs
pinning until the first dictionary addition.

Outcome: (B), declared in the entrypoint's `dang.toml` rather than in the
`[entrypoint]` table. The Dagger version Dang already reads there is the
module's engine version, so it pins the API view and naming together. A shared
remote entrypoint pins through the `dang.toml` of the directory it names. An
entrypoint that declares no version gets `v1.0.0-beta.20`, not the engine's
own version, so it no longer follows the engine.

### What changes for module authors (on bump)

A module that declares an `engineVersion` of `v1.0.0-beta.22` or later
(`latest`, a later prerelease, or `v1.0.0`, which a prerelease engine serves
at its own view) gets the new rules. Module type, field and argument names that
contain runs of capitals or dictionary terms get their real casing back:

- `MyModHttpclient` → `MyModHTTPClient`
- `Llmmessage` → `LLMMessage`, and references to core `LLM*` / `JSON*` types
  resolve to the core type instead of a namespaced copy.
- CLI flags: `E2ETest` → `--e2e-test` (was `--e-2-e-test`, which is still
  accepted).
- Fields, functions and arguments with digits or dictionary terms:
  `e2ETest` → `e2eTest`, `myK8SCluster` → `myK8sCluster`. Acronyms in fields
  stay written like words, as strcase wrote them (`baseUrl`, Python's
  `com_url` → `comUrl`).
- Enum members with digits or dictionary terms: `ARM_64` → `ARM64`,
  `GIT_HUB` → `GITHUB`.
- The module's client is served at the new view, so it sees the renamed core
  API (`commitSha`, `asJson`, `FunctionCachePolicy` `NEVER`; see
  [Core schema](#core-schema)), and a Go module's generated identifiers change
  as described in [SDK Integration](#sdk-integration).

Names that were already canonical don't change. The old schema names of the
others are gone: there are no aliases, so callers built against them (other
modules' code, committed bindings, hand-written queries) must move to the new
names with the bump. The CLI still accepts the old flag, command and enum
member spellings it printed before, on its side only.

### Core schema

Core names are written by hand in Go and are *not* run through the
normalizer. Measured against `docs/docs-graphql/schema.graphqls` before the
cut, with the initial dictionary, by the [schema casing
convention](#schema-casing-convention): every type name and input field
already followed it; 20 field and argument names wrote acronyms in capitals;
and three enums had values that weren't `SCREAMING_SNAKE`. These are renamed
at `engine.IdentifierCasingVersion`:

| type | before the cut | from the cut on |
| --- | --- | --- |
| `Cloud` | `traceURL` | `traceUrl` |
| `Container` | `from`/`publish(insecureSkipTLSVerify:)` | `insecureSkipTlsVerify` |
| `Container` | `withGPU`, `experimentalWithGPU`, `experimentalWithAllGPUs` | `withGpu`, `experimentalWithGpu`, `experimentalWithAllGpus` |
| `File` | `asJSON` | `asJson` |
| `GeneratedCode` | `withVCSGeneratedPaths`, `withVCSIgnoredPaths` | `withVcsGeneratedPaths`, `withVcsIgnoredPaths` |
| `GitBundle` | `prerequisiteSHAs` | `prerequisiteShas` |
| `GitCommit` | `treeSHA` | `treeSha` |
| `GitPushResult` | `previousSHA` | `previousSha` |
| `GitRef` | `commitSHA`, `push(expectedRemoteSHA:)` | `commitSha`, `expectedRemoteSha` |
| `LLM` | `withMCPServer` | `withMcpServer` |
| `Module` | `introspectionSchemaJSON` | `introspectionSchemaJson` |
| `ModuleSource` | `withSDK`, `introspectionSchemaJSON`, `clientSchemaIntrospectionJSON`, `htmlURL`, `htmlRepoURL` | `withSdk`, `introspectionSchemaJson`, `clientSchemaIntrospectionJson`, `htmlUrl`, `htmlRepoUrl` |
| `Workspace` | `withSDK`, `withoutSDK` | `withSdk`, `withoutSdk` |
| `FunctionCachePolicy` | `Default`, `PerSession`, `Never` | `DEFAULT`, `PER_SESSION`, `NEVER` |
| `ImageLayerCompression` | `Gzip`, `Zstd`, `EStarGZ`, `Uncompressed` (`GZIP`, ... as aliases) | `GZIP`, `ZSTD`, `ESTARGZ`, `UNCOMPRESSED` |
| `ImageMediaTypes` | `OCIMediaTypes`, `DockerMediaTypes` (`OCI`, `DOCKER` as aliases) | `OCI`, `DOCKER` |

Names that already followed the convention (`shortSha`, `parentShas`,
`filterUri`, `callId`, `pushUrl`, `asSdkName`) don't change. The legacy
`load<Type>FromID` loaders, which only exist before v0.22.0, are left as they
are. Doc strings aren't names and don't follow the convention: the `@cache`
directive's `ttl` doc still says "Default policy".

**Fields and arguments** are plain view gates, with no aliases and no
`@deprecated` duplicates: no view has both spellings. Each is defined once
under its new name and installed twice (`core/schema/renamed_names.go`): under
the new name in `core.CasedNamesView` (`AfterVersion(cut)`, which includes the
internal view `""`) and under the old name in `core.LegacyNamesView`
(`BeforeVersion(cut)`), each intersected with the views the name had before.
A renamed field that had no view filter is view-scoped now, so its calls
record the caller's view rather than `""`. `Container.from` and `publish`
have no view filter, so dagql selects every call to them in view `""`; there
the old `insecureSkipTLSVerify` also stays, as an internal argument, so a
pre-cut caller's flag isn't dropped, and the resolver takes either spelling.
Engine-internal selections (workspace reset, `Container.from`'s re-select, the
CLI's own queries) use the new names; the persisted Container recipe decoder
takes both GPU spellings, and the TUI's checkpoint decoder both
`withMcpServer` spellings.

**Enum values.** The `SCREAMING_SNAKE` spelling is the stored value: core code
compares it, IDs record it, and values persisted with an old spelling (a
typedef's cache policy, an image export's media types) are normalized when
decoded. The old spellings are view-gated aliases
(`dagql.EnumValues.AliasViewPrimary`): views before the cut still list them as
the members, with the `SCREAMING_SNAKE` spellings as their aliases, so pre-cut
schemas are unchanged byte for byte; views from the cut on have only the
`SCREAMING_SNAKE` spellings. Every spelling decodes to the value in every
view, so recipes recorded with an old spelling still load, though their IDs
differ, so their cache keys miss once. Values going out are spelled for the
receiver's view:

- argument defaults in the schema (`mediaTypes = OCI` from the cut on,
  `OCIMediaTypes` before; `dagql.ViewLiteraler`);
- enum leaves of query results, for the client's view;
- core enum arguments passed into a module function, for the module's view
  (`CoreModEnum.ConvertToSDKInput`), the view of its `engineVersion` by
  `engine.ModuleAPIViewVersion`: a module that declares none gets the engine's
  own view, one declaring a version past the engine's gets the engine's view
  too, and one whose version isn't semver counts as v0.11.9. A module whose
  runtime client declares its own version (`DAGGER_CLIENT_VERSION`) still
  gets them spelled for that view, not the declared client's: the engine
  converts them before the runtime connects. Every spelling decodes in every
  view, so only code comparing the raw value with its SDK's constant can
  tell.

**Guard tests.** `TestCoreSchemaGraphQLCasing` (`core/schema`) checks every
type, field, argument, input field and enum value of the live schema's views
from the cut on, and `TestCoreSchemaConventional` (`engine/naming`) does the
same over `docs/docs-graphql/schema.graphqls`; both exception lists are
empty. `TestCoreRenamedNamesViews` checks that views before the cut have only
the old names and later views only the new ones, and
`TestRenamedEnumPreCutSchema` that pre-cut enum schemas are unchanged.

### Accepted breaks

- **Old generated clients under a newer session.** A host client generated
  before the cut, run under a newer outer session (a newer CLI), gets the
  newer view, so its calls to renamed fields, arguments and enum values fail.
  Pre-cut modules keep their own view and keep working.
- **Recordings from pre-cut engines.** A recipe that a pre-cut engine
  recorded and that uses a renamed field can't be rebuilt by a post-cut
  engine: agent checkpoints, `dagger restore` from a Cloud trace, stored
  recipe IDs. dagql resolves each frame's field by name in its recorded view,
  so the load fails, naming the field. This hits calls to fields that had no
  view filter, which were recorded in view `""`, and every call by a v1
  prerelease caller from an engine before prerelease-granular views (#14810),
  which recorded them at view `v1.0.0`, now past the cut. Persisted cache
  entries are reached by digest, so for the cache this is only a miss. There
  are no load-time aliases.
- **Dependency clients across the cut.** An SDK that generates a module's
  dependency clients from
  `withEngineVersion(consumer).clientSchemaIntrospectionJson` names them by
  the consumer's version, so a dependency on the other side of the cut can be
  named by other rules than its bindings assume. An open follow-up adds an
  argument to `clientSchemaIntrospectionJson` to pick the version per
  dependency.

## Guarantees

Checked against the [test vectors](#test-vectors) and the full core schema by
the `engine/naming` tests.

1. **Round trip.** If every acronym and term in `x` is in the dictionary, then
   `parse(format(parse(x), C, S))` has the same words as `parse(x)`, for every
   casing `C` except `FLAT` and both styles `S`. Acronyms the dictionary doesn't
   know survive only formats that keep capitals (`PASCAL`, and `CAMEL` after the
   first word, with `UPPERCASE`).
2. **Idempotence.** Formatting the result again changes nothing:
   `format(parse(format(parse(x), C, S)), C, S)` equals
   `format(parse(x), C, S)` for `PASCAL`, `CAMEL`, `SNAKE` and
   `SCREAMING_SNAKE`, in either style `S`.
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
| An entry plus `s` spells another entry | `httpS3` → `HTTPS`+`3`, since `HTTP`·`s3` is `HTTPS3` in `PASCAL`; `readIOs` → `iOS` | write `http_s3`, or avoid the name |
| `FLAT` | drops boundaries | output only |

## Test Vectors

These start the shared test-case file, `engine/naming/testdata/vectors.json`,
which the `engine/naming` tests check the package against. The in-repo Go
generator imports `engine/naming`, and the other SDKs keep their own
converters, so none of them carries a copy.
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
| `APIv1` | ^API · v1 | `APIV1` | `apiV1` | `api_v1` | `ApiV1` |
| `UUIDV7` | ^UUID · v7 | `UUIDV7` | `uuidV7` | `uuid_v7` | `UuidV7` |
| `JSONV2Schema` | ^JSON · v2 · schema | `JSONV2Schema` | `jsonV2Schema` | `json_v2_schema` | `JsonV2Schema` |
| `json_v2_schema` | ^JSON · v2 · schema | `JSONV2Schema` | `jsonV2Schema` | `json_v2_schema` | `JsonV2Schema` |
| `apiS3Bucket` | ^API · s3 · bucket | `APIS3Bucket` | `apiS3Bucket` | `api_s3_bucket` | `ApiS3Bucket` |
| `HTTP2s` | ^HTTP+2s | `HTTP2s` | `http2s` | `http2s` | `Http2s` |
| `SHA256s` | ^SHA+256s | `SHA256s` | `sha256s` | `sha256s` | `Sha256s` |
| `K8STools` | *K8s · tools | `K8sTools` | `k8sTools` | `k8s_tools` | `K8sTools` |
| `K8sTools` | *K8s · tools | `K8sTools` | `k8sTools` | `k8s_tools` | `K8sTools` |
| `k8s-tools` | *K8s · tools | `K8sTools` | `k8sTools` | `k8s_tools` | `K8sTools` |
| `K8S_TOOLS` | *K8s · tools | `K8sTools` | `k8sTools` | `k8s_tools` | `K8sTools` |
| `MyK8SCluster` | my · *K8s · cluster | `MyK8sCluster` | `myK8sCluster` | `my_k8s_cluster` | `MyK8sCluster` |
| `K3SServer` | *K3s · server | `K3sServer` | `k3sServer` | `k3s_server` | `K3sServer` |
| `k3s_server` | *K3s · server | `K3sServer` | `k3sServer` | `k3s_server` | `K3sServer` |
| `Café` | error: non-ASCII | | | | |

## Open Questions

1. **.NET's two-letter rule** (`IOStream` but `HtmlTag`; `Id` and `Ok` are
   exceptions). It only affects C#, F# and VB, and only two-letter acronyms
   other than `ID`. In the core schema that is `GZ`, in an enum value the .NET
   SDK writes as is, so it changes no C# name. Proposal: leave
   it out. .NET uses `CAPITALIZED`, and `Identifier.words` carries what's needed
   if the .NET SDK ever wants the exact form. A third `AcronymStyle` value can be
   added later without breaking anyone.
2. **Renaming the non-conventional core names** before 1.0, with deprecated
   aliases, or keeping them on an allowlist. Resolved: they're renamed at the
   cut by plain view gates, with no aliases and no deprecated duplicates (see
   [Core schema](#core-schema)).
3. **Offline codegen.** Nearly all codegen has an engine connection, or reads
   its input from a step that has one, so SDK codegen can ask the engine to
   format names with `formatIdentifiers` rather than carry a parser. The
   in-repo Go generator imports `engine/naming` instead; no other in-repo
   generator formats names through the engine.
4. **Splitting all-caps input** (`E2EAPI`) by full dictionary cover, as for
   caps pieces. Resolved: each chunk of all-caps input splits only if
   dictionary entries (and digit runs) cover it completely; lowercase input
   never splits, because the same rule on lowercase words risks false
   positives. Without it, `PASCAL` names made only of acronyms (`JSONAPI`,
   `HTMLURL`) wouldn't come back unchanged.
5. **Java and PHP class names.** Strict "when in Rome" would write them
   `CAPITALIZED` (`JsonValue`, `Llm` in Java; `JsonValue` throughout in PHP).
   A case-only class rename can't keep a deprecated alias, so this would be a
   breaking change. Resolved for Java (and .NET): taken before 1.0, as hard
   breaks. Not part of this stack, which changes no SDK's class names.
6. **TypeScript's convention.** The ecosystem writes acronyms both ways.
   Resolved: members follow the schema's field names (`commitSha()`), and enum
   members keep the `PASCAL`/`CAPITALIZED` form the generator always wrote.
7. **Input object field names.** Rust and Elixir (and possibly Python) have
   existing bugs where an input object field is sent by its SDK name rather
   than its schema name. That's a separate fix (#14636); no core input field
   is renamed.
8. **Dependency clients across the cut** (see
   [Accepted breaks](#accepted-breaks)). Open: a follow-up adds an argument to
   `clientSchemaIntrospectionJson` to pick the version a dependency's client
   is named by.
9. **Deprecated aliases for Go identifiers** derived from dependency module
   names, which the Go generator reformats past the cut (`Cli` → `CLI`).
   Resolved: none; a Go module that bumps its `engineVersion` updates its
   callers.

## Implementation Plan

1. The `engine/naming` package: parser, formatter, dictionary, and the
   test-vector file as table tests, with property tests for the guarantees.
2. The core API (`identifier`, `formatIdentifiers`, `namingDictionary`).
3. Prerelease-granular API views, so the cut can sit at `v1.0.0-beta.22`
   (`engine.IdentifierCasingVersion`) (#14810).
4. Module naming by the declared engine version: `core/gqlformat.go`, typedef
   constructors, `namespaceObject`, CLI commands and flags; the identifier API
   on every v1 view (#14891).
5. The core renames at the cut, enum values spelled for the receiver's view,
   Go codegen by `engine/naming`, and the in-repo clients regenerated
   (#14895).

Steps 1 and 2 are merged; 3–5 are a stack of drafts. Not done:

- deleting `strcase.ConfigureAcronym` in `core/llm.go` / `core/json.go` and
  the `withFinalTypeName` workaround, which wait on dropping modules below
  the cut;
- pinning an entrypoint module's naming version, which waits on a manifest v2
  signal (see [Module entrypoints](#module-entrypoints));
- a version argument for `clientSchemaIntrospectionJson` (see
  [Accepted breaks](#accepted-breaks));
- the Go module self-binding names (see the Known gaps under
  [The gate](#the-gate));
- the extracted SDKs (`github.com/dagger/{go,python,typescript,php,java,dang}-sdk`,
  `core/sdk/sdkmeta/sdks.json`), which keep their own converters, and also
  generate the manifest v2 entrypoints where pinned naming would land.
