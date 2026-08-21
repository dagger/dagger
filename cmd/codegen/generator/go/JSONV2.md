# json/v2 support in the Go SDK: design and Go 1.27 migration

The Go SDK's `Marshalers`/`Unmarshalers` (and, in portable API mode, the
`MarshalJSON`/`UnmarshalJSON` helpers) are built on json/v2. Until the
repository's minimum Go version is 1.27, json/v2 exists in two forms:

- stdlib `encoding/json/v2` — stable in Go 1.27; opt-in via
  `GOEXPERIMENT=jsonv2` on Go 1.25/1.26,
- `github.com/go-json-experiment/json` — same API, maintained by the stdlib
  json/v2 author; under `goexperiment.jsonv2 && go1.25` it compiles to pure
  type aliases of the stdlib package, so the two are type-identical there.

The generated code selects between them with the `goexperiment.jsonv2` build
tag, in two file pairs rendered from the shared body
`templates/src/_jsonv2/marshalers.go.tmpl`:

| template | build tag | json/v2 package |
|---|---|---|
| `src/jsonv2.gen.go.tmpl`, `src/internal/dagger/jsonv2.gen.go.tmpl` | `goexperiment.jsonv2` | `encoding/json/v2` |
| `src/jsonv2_compat.gen.go.tmpl`, `src/internal/dagger/jsonv2_compat.gen.go.tmpl` | `!goexperiment.jsonv2` | `github.com/go-json-experiment/json` |

## TODO(go1.27): steps when the minimum Go version reaches 1.27

This split becomes unnecessary once `go >= 1.27` in `sdk/go/go.mod` (stdlib
json/v2 is then always available). The migration is mechanical and changes no
API (the two packages' types are identical under the experiment):

1. Delete both `jsonv2_compat.gen.go.tmpl` templates. In the remaining
   `jsonv2.gen.go.tmpl` templates, remove the `//go:build goexperiment.jsonv2`
   constraint (it is on by default in 1.27; the `GOEXPERIMENT=nojsonv2` escape
   hatch is scheduled for removal upstream).
2. Update doc comments in `src/_jsonv2/marshalers.go.tmpl` that mention
   go-json-experiment or the build tag.
3. Regenerate `sdk/go`: `jsonv2.gen.go` loses the build tag,
   `jsonv2_compat.gen.go` is deleted. Update the `GoSDK` embed pattern in
   `sdk/go/fs.go` accordingly.
4. Drop `github.com/go-json-experiment/json` from `sdk/go/go.mod`/`go.sum`
   and from the root `go.mod`/`go.sum` (the root replaces `dagger.io/dagger`
   with `./sdk/go`), then `go mod tidy` both.
5. Grep for remaining `go-json-experiment` references (docs, comments,
   engine-dev baked module cache) and remove them, including this section.
6. Regenerate committed fixture modules containing `internal/dagger/jsonv2*`
   files.

Verification: `go build ./...` in `sdk/go`, codegen golden/regen checks, and
a module regeneration round-trip.
