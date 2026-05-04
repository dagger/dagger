# moduleTypes Consolidation — PR 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship PR 1 of the moduleTypes consolidation: new `cmd/codegen` schema-JSON subcommands (`inspect-schema`, `merge-schema`), AST-based Go source analyzer, rewired `generate-module` that runs Phase 1 (AST scan) + Phase 2 (schema merge) + Phase 3 (bindings generation) in a single pass. Go SDK engine-side stops advertising `moduleTypes` and lets the engine fall through to the empty-function-name path.

**Architecture:** New package `cmd/codegen/schematool/` owns pure JSON schema inspection and merging, usable both as a library and via CLI. New package `cmd/codegen/generator/go/astscan/` does AST-only source analysis for Go modules. `generate-module` wires them together in-process; legacy `packages.Load` path is retained behind a build tag + `--legacy-typedefs` flag for rollback. Go SDK's `AsModuleTypes` is stubbed to `nil, false` so the engine takes the empty-fn-name fallback for Go — zero engine-code changes in this PR.

**Tech Stack:** Go 1.23+, `go/parser`, `go/ast`, `go/token`, cobra CLI, existing `cmd/codegen/introspection` types, stgit patches.

**Reference:** the spec at `hack/designs/no-codegen-at-runtime-moduletypes.md`. The exact commit breakdown and rollout strategy this plan implements comes from the "Rollout plan → PR 1" section of that spec.

---

## Prerequisites

Before starting:

- Working directory: `/home/yves/dev/src/github.com/dagger/dagger-worktrees/no-codegen-at-runtime`
- Branch: `no-codegen-at-runtime`
- The design doc is already committed as the stg patch `no-codegen-at-runtime-design`.
- Every commit below is an **stg patch** (`stg new -m "..."` → edit files → `stg refresh`). Never use plain `git commit` on this branch.
- Every patch message ends with `Signed-off-by: Yves Brissaud <yves@dagger.io>`.
- Never add `Co-Authored-By` lines.
- Never run `git push` without explicit approval.
- Verify baseline is green before starting: `go test ./cmd/codegen/...` must pass.

---

## File Structure (PR 1)

### New files

| Path | Responsibility |
|---|---|
| `cmd/codegen/schematool/schematool.go` | Public API: `Merge`, `ListTypes`, `HasType`, `DescribeType` |
| `cmd/codegen/schematool/module_types.go` | `ModuleTypes` input struct — language-agnostic JSON shape of a module's declared types |
| `cmd/codegen/schematool/merge.go` | Internal merge implementation (append ModuleTypes into `introspection.Schema`) |
| `cmd/codegen/schematool/inspect.go` | Internal inspect implementation (list/has/describe against `introspection.Schema`) |
| `cmd/codegen/schematool/schematool_test.go` | Unit tests for public API |
| `cmd/codegen/schematool/testdata/<case>/` | Golden fixtures: `schema.json`, `module_types.json`, `expected.json` |
| `cmd/codegen/inspect_schema.go` | Cobra command `inspect-schema` with subcommands (`list-types`, `has-type`, `describe-type`) |
| `cmd/codegen/merge_schema.go` | Cobra command `merge-schema` |
| `cmd/codegen/generator/go/astscan/scanner.go` | `Scan(dir string, schema *introspection.Schema) (*schematool.ModuleTypes, error)` |
| `cmd/codegen/generator/go/astscan/resolve.go` | AST type-expression → `introspection.TypeRef` resolution (against imports + schema) |
| `cmd/codegen/generator/go/astscan/scanner_test.go` | Table-driven tests against fixture packages |
| `cmd/codegen/generator/go/astscan/testdata/<case>/` | Fixture Go packages + `expected.json` |
| `cmd/codegen/generator/go/generate_module_legacy.go` | Old `packages.Load` path under `//go:build legacy_typedefs` |
| `cmd/codegen/generator/go/generate_typedefs_legacy.go` | Old `generate-typedefs` extraction body under same build tag |

### Modified files

| Path | Change |
|---|---|
| `cmd/codegen/main.go` | Register `inspectSchemaCmd` + `mergeSchemaCmd` |
| `cmd/codegen/generator/go/generate_module.go` | Default path uses astscan + schematool in-process; legacy path dispatched when `--legacy-typedefs` is set |
| `cmd/codegen/generator/go/generate_typedefs.go` | Kept buildable only under legacy tag (moved into `_legacy.go`); removed from default build |
| `cmd/codegen/generate_module.go` | Accept new `--legacy-typedefs` flag; wire through to `ModuleGeneratorConfig` |
| `cmd/codegen/generator/config.go` | Add `LegacyTypedefs bool` to `ModuleGeneratorConfig` |
| `core/sdk/go_sdk.go` | `AsModuleTypes` returns `nil, false`; `ModuleTypes` method body deleted |
| `core/integration/module_test.go` | Add parity test `TestGoCodegenPhase1Parity` and self-calls green check |

### Deleted files (in PR 1)

None. PR 2 handles deletion of legacy path.

---

## Commit 1 — schematool package foundation

**Patch name:** `schematool-foundation`

**Intent:** land the `schematool` library + CLI subcommands with thorough unit tests. No callers yet; the code is dormant but exercisable via CLI.

### Task 1.1: Create new stg patch

- [ ] **Step 1: Start the patch**

```bash
stg new -m "cmd/codegen: add schematool package for schema inspect + merge

Introduce a schema-manipulation library and two new subcommands
(inspect-schema, merge-schema) usable by any SDK that needs to
reason about introspection JSON without depending on language-
specific tooling.

Signed-off-by: Yves Brissaud <yves@dagger.io>" schematool-foundation
```

- [ ] **Step 2: Confirm patch is top of stack**

Run: `stg top`
Expected: `schematool-foundation`

### Task 1.2: Define ModuleTypes input shape

**Files:**

- Create: `cmd/codegen/schematool/module_types.go`

- [ ] **Step 1: Write the input struct**

```go
// Package schematool manipulates Dagger introspection JSON.
// It is consumed both as a Go library (by the Go SDK's codegen,
// which runs in-process) and as a CLI by other SDKs.
//
// See hack/designs/no-codegen-at-runtime-moduletypes.md for the
// design rationale.
package schematool

import (
    "encoding/json"
    "fmt"
    "io"
)

// ModuleTypes is the language-agnostic input shape that a SDK's
// Phase-1 source analyzer produces for Merge. It is a minimal
// subset of core.Module: the module's name plus the types it
// declares.
type ModuleTypes struct {
    Name        string        `json:"name"`
    Description string        `json:"description,omitempty"`
    Objects     []ObjectDef    `json:"objects,omitempty"`
    Interfaces  []InterfaceDef `json:"interfaces,omitempty"`
    Enums       []EnumDef      `json:"enums,omitempty"`
}

// ObjectDef mirrors core.ObjectTypeDef to the extent the SDK needs
// to expose via introspection.
type ObjectDef struct {
    Name        string     `json:"name"`
    Description string     `json:"description,omitempty"`
    Constructor *Function  `json:"constructor,omitempty"`
    Functions   []Function `json:"functions,omitempty"`
    Fields      []FieldDef `json:"fields,omitempty"`
}

type InterfaceDef struct {
    Name        string     `json:"name"`
    Description string     `json:"description,omitempty"`
    Functions   []Function `json:"functions,omitempty"`
}

type EnumDef struct {
    Name        string     `json:"name"`
    Description string     `json:"description,omitempty"`
    Values      []EnumValue `json:"values"`
}

type EnumValue struct {
    Name        string `json:"name"`
    Description string `json:"description,omitempty"`
    Value       string `json:"value,omitempty"`
}

type FieldDef struct {
    Name        string   `json:"name"`
    Description string   `json:"description,omitempty"`
    TypeRef     *TypeRef `json:"type"`
}

type Function struct {
    Name        string    `json:"name"`
    Description string    `json:"description,omitempty"`
    Args        []FuncArg `json:"args,omitempty"`
    ReturnType  *TypeRef  `json:"returnType"`
}

type FuncArg struct {
    Name         string   `json:"name"`
    Description  string   `json:"description,omitempty"`
    TypeRef      *TypeRef `json:"type"`
    DefaultValue *string  `json:"defaultValue,omitempty"`
}

// TypeRef is a reference to a type by name, optionally nested
// (list / non-null). Mirrors introspection.TypeRef but carries
// only the fields SDK-produced JSON needs to set.
type TypeRef struct {
    Kind    string   `json:"kind"` // OBJECT, INTERFACE, ENUM, SCALAR, LIST, NON_NULL
    Name    string   `json:"name,omitempty"`
    OfType  *TypeRef `json:"ofType,omitempty"`
}

// DecodeModuleTypes reads a ModuleTypes JSON value from r.
func DecodeModuleTypes(r io.Reader) (*ModuleTypes, error) {
    var mt ModuleTypes
    dec := json.NewDecoder(r)
    dec.DisallowUnknownFields()
    if err := dec.Decode(&mt); err != nil {
        return nil, fmt.Errorf("decode module types: %w", err)
    }
    return &mt, nil
}
```

- [ ] **Step 2: Stage the file**

```bash
git add cmd/codegen/schematool/module_types.go
```

- [ ] **Step 3: Verify it compiles**

Run: `go build ./cmd/codegen/schematool/...`
Expected: no output (clean build).

### Task 1.3: Write failing test for Merge — single object

**Files:**

- Create: `cmd/codegen/schematool/testdata/single_object/schema.json`
- Create: `cmd/codegen/schematool/testdata/single_object/module_types.json`
- Create: `cmd/codegen/schematool/testdata/single_object/expected.json`
- Create: `cmd/codegen/schematool/schematool_test.go`

- [ ] **Step 1: Create a minimal introspection fixture**

`cmd/codegen/schematool/testdata/single_object/schema.json`:

```json
{
  "__schema": {
    "queryType": { "name": "Query" },
    "types": [
      {
        "kind": "OBJECT",
        "name": "Query",
        "fields": [],
        "interfaces": [],
        "directives": []
      },
      {
        "kind": "OBJECT",
        "name": "Container",
        "fields": [],
        "interfaces": [],
        "directives": []
      }
    ],
    "directives": []
  },
  "__schemaVersion": "test"
}
```

- [ ] **Step 2: Create the module types input**

`cmd/codegen/schematool/testdata/single_object/module_types.json`:

```json
{
  "name": "echo",
  "description": "Echo module",
  "objects": [
    {
      "name": "Echo",
      "description": "Echo object",
      "functions": [
        {
          "name": "say",
          "description": "Say something",
          "args": [
            { "name": "msg", "type": { "kind": "NON_NULL", "ofType": { "kind": "SCALAR", "name": "String" } } }
          ],
          "returnType": { "kind": "NON_NULL", "ofType": { "kind": "SCALAR", "name": "String" } }
        }
      ]
    }
  ]
}
```

- [ ] **Step 3: Create the expected merged schema**

`cmd/codegen/schematool/testdata/single_object/expected.json`:

```json
{
  "__schema": {
    "queryType": { "name": "Query" },
    "types": [
      {
        "kind": "OBJECT",
        "name": "Query",
        "fields": [],
        "interfaces": [],
        "directives": []
      },
      {
        "kind": "OBJECT",
        "name": "Container",
        "fields": [],
        "interfaces": [],
        "directives": []
      },
      {
        "kind": "OBJECT",
        "name": "Echo",
        "description": "Echo object",
        "fields": [
          {
            "name": "say",
            "description": "Say something",
            "args": [
              { "name": "msg", "type": { "kind": "NON_NULL", "ofType": { "kind": "SCALAR", "name": "String" } } }
            ],
            "type": { "kind": "NON_NULL", "ofType": { "kind": "SCALAR", "name": "String" } }
          }
        ],
        "interfaces": [],
        "directives": [
          {
            "name": "sourceModuleName",
            "args": [{ "name": "name", "value": "\"echo\"" }]
          }
        ]
      }
    ],
    "directives": []
  },
  "__schemaVersion": "test"
}
```

- [ ] **Step 4: Write the failing test**

`cmd/codegen/schematool/schematool_test.go`:

```go
package schematool_test

import (
    "bytes"
    "encoding/json"
    "os"
    "path/filepath"
    "testing"

    "github.com/dagger/dagger/cmd/codegen/introspection"
    "github.com/dagger/dagger/cmd/codegen/schematool"
)

func TestMerge(t *testing.T) {
    cases := []string{
        "single_object",
    }
    for _, name := range cases {
        t.Run(name, func(t *testing.T) {
            dir := filepath.Join("testdata", name)

            schema := loadSchema(t, filepath.Join(dir, "schema.json"))
            modTypes := loadModuleTypes(t, filepath.Join(dir, "module_types.json"))

            if err := schematool.Merge(schema, modTypes); err != nil {
                t.Fatalf("merge: %v", err)
            }

            got := marshal(t, schema)
            want := readFile(t, filepath.Join(dir, "expected.json"))
            assertJSONEqual(t, got, want)
        })
    }
}

func loadSchema(t *testing.T, path string) *introspection.Schema {
    t.Helper()
    data := readFile(t, path)
    var resp introspection.Response
    if err := json.Unmarshal(data, &resp); err != nil {
        t.Fatalf("unmarshal schema: %v", err)
    }
    return resp.Schema
}

func loadModuleTypes(t *testing.T, path string) *schematool.ModuleTypes {
    t.Helper()
    data := readFile(t, path)
    mt, err := schematool.DecodeModuleTypes(bytes.NewReader(data))
    if err != nil {
        t.Fatalf("decode module types: %v", err)
    }
    return mt
}

func marshal(t *testing.T, schema *introspection.Schema) []byte {
    t.Helper()
    out := struct {
        Schema        *introspection.Schema `json:"__schema"`
        SchemaVersion string                `json:"__schemaVersion"`
    }{Schema: schema, SchemaVersion: "test"}
    b, err := json.Marshal(out)
    if err != nil {
        t.Fatalf("marshal: %v", err)
    }
    return b
}

func readFile(t *testing.T, path string) []byte {
    t.Helper()
    b, err := os.ReadFile(path)
    if err != nil {
        t.Fatalf("read %s: %v", path, err)
    }
    return b
}

// assertJSONEqual compares two JSON byte slices after canonical
// re-marshalling so formatting differences don't cause false negatives.
func assertJSONEqual(t *testing.T, got, want []byte) {
    t.Helper()
    var g, w any
    if err := json.Unmarshal(got, &g); err != nil {
        t.Fatalf("unmarshal got: %v", err)
    }
    if err := json.Unmarshal(want, &w); err != nil {
        t.Fatalf("unmarshal want: %v", err)
    }
    gb, _ := json.MarshalIndent(g, "", "  ")
    wb, _ := json.MarshalIndent(w, "", "  ")
    if !bytes.Equal(gb, wb) {
        t.Errorf("json mismatch\n---got---\n%s\n---want---\n%s", gb, wb)
    }
}
```

- [ ] **Step 5: Run it and verify failure**

Run: `go test ./cmd/codegen/schematool/ -run TestMerge -v`
Expected: fails with compile error (`schematool.Merge undefined`). That's the point.

### Task 1.4: Implement minimal Merge to pass single_object

**Files:**

- Create: `cmd/codegen/schematool/merge.go`
- Create: `cmd/codegen/schematool/schematool.go`

- [ ] **Step 1: Write `schematool.go` public API skeleton**

```go
package schematool

import "github.com/dagger/dagger/cmd/codegen/introspection"

// Merge appends the types declared in mod to schema. If a type name
// in mod collides with an existing type in schema, Merge returns an
// error.
//
// Merge preserves directive metadata by stamping each inserted type
// with a @sourceModuleName directive carrying mod.Name.
func Merge(schema *introspection.Schema, mod *ModuleTypes) error {
    return mergeInto(schema, mod)
}

// ListTypes returns the names of types in schema matching the given
// kind filter. An empty kind matches all types.
func ListTypes(schema *introspection.Schema, kind string) []string {
    return listTypes(schema, kind)
}

// HasType reports whether schema contains a type with the given name.
func HasType(schema *introspection.Schema, name string) bool {
    return schema.Types.Get(name) != nil
}

// DescribeType returns the schema.Type with the given name, or nil.
func DescribeType(schema *introspection.Schema, name string) *introspection.Type {
    return schema.Types.Get(name)
}
```

- [ ] **Step 2: Write minimal `merge.go`**

```go
package schematool

import (
    "fmt"

    "github.com/dagger/dagger/cmd/codegen/introspection"
)

func mergeInto(schema *introspection.Schema, mod *ModuleTypes) error {
    if mod == nil {
        return fmt.Errorf("module types: nil")
    }
    for _, obj := range mod.Objects {
        if schema.Types.Get(obj.Name) != nil {
            return fmt.Errorf("type %q already exists in schema", obj.Name)
        }
        schema.Types = append(schema.Types, convertObject(obj, mod.Name))
    }
    for _, iface := range mod.Interfaces {
        if schema.Types.Get(iface.Name) != nil {
            return fmt.Errorf("type %q already exists in schema", iface.Name)
        }
        schema.Types = append(schema.Types, convertInterface(iface, mod.Name))
    }
    for _, enum := range mod.Enums {
        if schema.Types.Get(enum.Name) != nil {
            return fmt.Errorf("type %q already exists in schema", enum.Name)
        }
        schema.Types = append(schema.Types, convertEnum(enum, mod.Name))
    }
    return nil
}

func convertObject(obj ObjectDef, modName string) *introspection.Type {
    t := &introspection.Type{
        Kind:        introspection.TypeKindObject,
        Name:        obj.Name,
        Description: obj.Description,
        Interfaces:  []*introspection.Type{},
        Directives:  moduleDirectives(modName),
    }
    for _, fn := range obj.Functions {
        t.Fields = append(t.Fields, convertFunction(fn))
    }
    for _, f := range obj.Fields {
        t.Fields = append(t.Fields, &introspection.Field{
            Name:        f.Name,
            Description: f.Description,
            TypeRef:     convertTypeRef(f.TypeRef),
            Args:        introspection.InputValues{},
            Directives:  introspection.Directives{},
        })
    }
    return t
}

func convertInterface(iface InterfaceDef, modName string) *introspection.Type {
    t := &introspection.Type{
        Kind:        introspection.TypeKindInterface,
        Name:        iface.Name,
        Description: iface.Description,
        Interfaces:  []*introspection.Type{},
        Directives:  moduleDirectives(modName),
    }
    for _, fn := range iface.Functions {
        t.Fields = append(t.Fields, convertFunction(fn))
    }
    return t
}

func convertEnum(enum EnumDef, modName string) *introspection.Type {
    t := &introspection.Type{
        Kind:        introspection.TypeKindEnum,
        Name:        enum.Name,
        Description: enum.Description,
        Interfaces:  []*introspection.Type{},
        Directives:  moduleDirectives(modName),
    }
    for _, v := range enum.Values {
        t.EnumValues = append(t.EnumValues, introspection.EnumValue{
            Name:        v.Name,
            Description: v.Description,
        })
    }
    return t
}

func convertFunction(fn Function) *introspection.Field {
    f := &introspection.Field{
        Name:        fn.Name,
        Description: fn.Description,
        TypeRef:     convertTypeRef(fn.ReturnType),
        Args:        introspection.InputValues{},
        Directives:  introspection.Directives{},
    }
    for _, a := range fn.Args {
        iv := introspection.InputValue{
            Name:        a.Name,
            Description: a.Description,
            TypeRef:     convertTypeRef(a.TypeRef),
        }
        if a.DefaultValue != nil {
            iv.DefaultValue = a.DefaultValue
        }
        f.Args = append(f.Args, iv)
    }
    return f
}

func convertTypeRef(ref *TypeRef) *introspection.TypeRef {
    if ref == nil {
        return nil
    }
    return &introspection.TypeRef{
        Kind:   introspection.TypeKind(ref.Kind),
        Name:   ref.Name,
        OfType: convertTypeRef(ref.OfType),
    }
}

func moduleDirectives(modName string) introspection.Directives {
    return introspection.Directives{
        {
            Name: "sourceModuleName",
            Args: []introspection.DirectiveArg{
                {Name: "name", Value: fmt.Sprintf("%q", modName)},
            },
        },
    }
}
```

NOTE: `introspection.DirectiveArg` / `Directives` fields must exist; verify before writing (see Step 3). If the actual shape differs, adjust `moduleDirectives` to match.

- [ ] **Step 3: Check the actual introspection Directives shape**

Run: `grep -n "^type Directive\|^type Directives" cmd/codegen/introspection/*.go`
Expected: see concrete field names (e.g. `Directives []*Directive` with `type Directive struct{...}`)

If the shape differs from my guess, adjust `moduleDirectives` accordingly. Do not guess — match the real types.

- [ ] **Step 4: Create `inspect.go` placeholder**

`cmd/codegen/schematool/inspect.go`:

```go
package schematool

import "github.com/dagger/dagger/cmd/codegen/introspection"

func listTypes(schema *introspection.Schema, kind string) []string {
    var out []string
    for _, t := range schema.Types {
        if kind != "" && string(t.Kind) != kind {
            continue
        }
        out = append(out, t.Name)
    }
    return out
}
```

- [ ] **Step 5: Run the test**

Run: `go test ./cmd/codegen/schematool/ -run TestMerge -v`
Expected: PASS for `TestMerge/single_object`.

### Task 1.5: Add interface case

**Files:**

- Create: `cmd/codegen/schematool/testdata/interface/schema.json`
- Create: `cmd/codegen/schematool/testdata/interface/module_types.json`
- Create: `cmd/codegen/schematool/testdata/interface/expected.json`
- Modify: `cmd/codegen/schematool/schematool_test.go`

- [ ] **Step 1: Write the fixtures**

Fixtures follow the pattern from Task 1.3. Module declares an `Animal` interface with a `sound(): String!` method; schema is the same two-type baseline; expected has the new `INTERFACE` entry with `@sourceModuleName`.

- [ ] **Step 2: Add the case name to the test table**

```go
cases := []string{
    "single_object",
    "interface",
}
```

- [ ] **Step 3: Run**

Run: `go test ./cmd/codegen/schematool/ -run TestMerge/interface -v`
Expected: PASS.

### Task 1.6: Add enum case

**Files:** same pattern, fixture directory `cmd/codegen/schematool/testdata/enum/`

- [ ] **Step 1:** Write fixtures (enum `Status` with values `PENDING`, `ACTIVE`).
- [ ] **Step 2:** Add `"enum"` to the test cases slice.
- [ ] **Step 3:** Run: `go test ./cmd/codegen/schematool/ -run TestMerge/enum -v` → PASS.

### Task 1.7: Add conflict-detection case

**Files:** fixture directory `cmd/codegen/schematool/testdata/conflict/`, contains `schema.json` with a type named `Container` and `module_types.json` that also declares `Container`.

- [ ] **Step 1: Write a new test function**

```go
func TestMergeConflict(t *testing.T) {
    dir := filepath.Join("testdata", "conflict")
    schema := loadSchema(t, filepath.Join(dir, "schema.json"))
    modTypes := loadModuleTypes(t, filepath.Join(dir, "module_types.json"))

    err := schematool.Merge(schema, modTypes)
    if err == nil {
        t.Fatal("expected conflict error, got nil")
    }
    if !strings.Contains(err.Error(), "already exists") {
        t.Errorf("error does not mention conflict: %v", err)
    }
}
```

Add `"strings"` to imports.

- [ ] **Step 2: Run**

Run: `go test ./cmd/codegen/schematool/ -run TestMergeConflict -v`
Expected: PASS.

### Task 1.8: Inspect helper unit tests

**Files:**

- Modify: `cmd/codegen/schematool/schematool_test.go`

- [ ] **Step 1: Write tests for ListTypes, HasType, DescribeType**

```go
func TestInspect(t *testing.T) {
    schema := loadSchema(t, filepath.Join("testdata", "single_object", "expected.json"))

    t.Run("ListTypes all", func(t *testing.T) {
        got := schematool.ListTypes(schema, "")
        if len(got) < 3 {
            t.Errorf("expected >=3 types, got %d", len(got))
        }
    })
    t.Run("ListTypes filter", func(t *testing.T) {
        got := schematool.ListTypes(schema, "OBJECT")
        for _, name := range got {
            if schematool.DescribeType(schema, name).Kind != "OBJECT" {
                t.Errorf("filter returned non-OBJECT type %q", name)
            }
        }
    })
    t.Run("HasType", func(t *testing.T) {
        if !schematool.HasType(schema, "Echo") {
            t.Error("Echo should exist")
        }
        if schematool.HasType(schema, "Nonexistent") {
            t.Error("Nonexistent should not exist")
        }
    })
    t.Run("DescribeType", func(t *testing.T) {
        got := schematool.DescribeType(schema, "Echo")
        if got == nil {
            t.Fatal("Echo missing")
        }
        if got.Name != "Echo" {
            t.Errorf("wrong name: %s", got.Name)
        }
    })
}
```

- [ ] **Step 2: Run**

Run: `go test ./cmd/codegen/schematool/ -v`
Expected: all sub-tests PASS.

### Task 1.9: Wire `inspect-schema` CLI subcommand

**Files:**

- Create: `cmd/codegen/inspect_schema.go`
- Modify: `cmd/codegen/main.go`

- [ ] **Step 1: Write the command**

```go
package main

import (
    "encoding/json"
    "fmt"
    "os"

    "github.com/dagger/dagger/cmd/codegen/introspection"
    "github.com/dagger/dagger/cmd/codegen/schematool"
    "github.com/spf13/cobra"
)

var inspectSchemaCmd = &cobra.Command{
    Use:   "inspect-schema",
    Short: "Read-only queries against an introspection JSON file",
}

var (
    inspectKind     string
    inspectTypeName string
)

var inspectListTypesCmd = &cobra.Command{
    Use:  "list-types",
    RunE: runInspectListTypes,
}

var inspectHasTypeCmd = &cobra.Command{
    Use:  "has-type",
    RunE: runInspectHasType,
}

var inspectDescribeTypeCmd = &cobra.Command{
    Use:  "describe-type",
    RunE: runInspectDescribeType,
}

func init() {
    inspectListTypesCmd.Flags().StringVar(&inspectKind, "kind", "",
        "filter: OBJECT, INTERFACE, ENUM, SCALAR, INPUT_OBJECT")
    inspectHasTypeCmd.Flags().StringVar(&inspectTypeName, "name", "", "type name")
    _ = inspectHasTypeCmd.MarkFlagRequired("name")
    inspectDescribeTypeCmd.Flags().StringVar(&inspectTypeName, "name", "", "type name")
    _ = inspectDescribeTypeCmd.MarkFlagRequired("name")

    inspectSchemaCmd.AddCommand(inspectListTypesCmd)
    inspectSchemaCmd.AddCommand(inspectHasTypeCmd)
    inspectSchemaCmd.AddCommand(inspectDescribeTypeCmd)
}

func loadIntrospection() (*introspection.Schema, error) {
    if introspectionJSONPath == "" {
        return nil, fmt.Errorf("--introspection-json-path is required")
    }
    data, err := os.ReadFile(introspectionJSONPath)
    if err != nil {
        return nil, fmt.Errorf("read %s: %w", introspectionJSONPath, err)
    }
    var resp introspection.Response
    if err := json.Unmarshal(data, &resp); err != nil {
        return nil, fmt.Errorf("unmarshal: %w", err)
    }
    return resp.Schema, nil
}

func runInspectListTypes(cmd *cobra.Command, _ []string) error {
    schema, err := loadIntrospection()
    if err != nil {
        return err
    }
    names := schematool.ListTypes(schema, inspectKind)
    return json.NewEncoder(cmd.OutOrStdout()).Encode(names)
}

func runInspectHasType(cmd *cobra.Command, _ []string) error {
    schema, err := loadIntrospection()
    if err != nil {
        return err
    }
    has := schematool.HasType(schema, inspectTypeName)
    fmt.Fprintln(cmd.OutOrStdout(), has)
    if !has {
        os.Exit(1)
    }
    return nil
}

func runInspectDescribeType(cmd *cobra.Command, _ []string) error {
    schema, err := loadIntrospection()
    if err != nil {
        return err
    }
    t := schematool.DescribeType(schema, inspectTypeName)
    if t == nil {
        return fmt.Errorf("type %q not found", inspectTypeName)
    }
    return json.NewEncoder(cmd.OutOrStdout()).Encode(t)
}
```

- [ ] **Step 2: Register in `main.go`**

Modify `cmd/codegen/main.go`'s `init` — add before the flag declarations:

```go
    rootCmd.AddCommand(inspectSchemaCmd)
    rootCmd.AddCommand(mergeSchemaCmd)
```

- [ ] **Step 3: Verify builds**

Run: `go build ./cmd/codegen/...`
Expected: fails because `mergeSchemaCmd` undefined. That's fine — we fill it in Task 1.10.

### Task 1.10: Wire `merge-schema` CLI subcommand

**Files:**

- Create: `cmd/codegen/merge_schema.go`

- [ ] **Step 1: Write the command**

```go
package main

import (
    "encoding/json"
    "fmt"
    "os"

    "github.com/dagger/dagger/cmd/codegen/introspection"
    "github.com/dagger/dagger/cmd/codegen/schematool"
    "github.com/spf13/cobra"
)

var (
    mergeModuleTypesPath string
    mergeOutputPath      string
)

var mergeSchemaCmd = &cobra.Command{
    Use:   "merge-schema",
    Short: "Merge module-defined types into an introspection JSON",
    RunE:  runMergeSchema,
}

func init() {
    mergeSchemaCmd.Flags().StringVar(&mergeModuleTypesPath, "module-types-path", "",
        "path to module types JSON (produced by SDK Phase-1 source analysis)")
    _ = mergeSchemaCmd.MarkFlagRequired("module-types-path")
    mergeSchemaCmd.Flags().StringVar(&mergeOutputPath, "output-path", "",
        "path to write the merged introspection JSON (default: stdout)")
}

func runMergeSchema(cmd *cobra.Command, _ []string) error {
    if introspectionJSONPath == "" {
        return fmt.Errorf("--introspection-json-path is required")
    }
    introspectionData, err := os.ReadFile(introspectionJSONPath)
    if err != nil {
        return fmt.Errorf("read introspection JSON: %w", err)
    }
    var resp introspection.Response
    if err := json.Unmarshal(introspectionData, &resp); err != nil {
        return fmt.Errorf("unmarshal introspection JSON: %w", err)
    }
    modData, err := os.ReadFile(mergeModuleTypesPath)
    if err != nil {
        return fmt.Errorf("read module types: %w", err)
    }
    var mod schematool.ModuleTypes
    if err := json.Unmarshal(modData, &mod); err != nil {
        return fmt.Errorf("unmarshal module types: %w", err)
    }
    if err := schematool.Merge(resp.Schema, &mod); err != nil {
        return fmt.Errorf("merge: %w", err)
    }
    out, err := json.Marshal(resp)
    if err != nil {
        return fmt.Errorf("marshal: %w", err)
    }
    if mergeOutputPath == "" {
        _, err := cmd.OutOrStdout().Write(out)
        return err
    }
    return os.WriteFile(mergeOutputPath, out, 0o600)
}
```

- [ ] **Step 2: Verify builds**

Run: `go build ./cmd/codegen/...`
Expected: clean build.

### Task 1.11: CLI smoke test

**Files:** none modified; uses the fixtures from Task 1.3.

- [ ] **Step 1: Build the binary**

```bash
go build -o /tmp/codegen ./cmd/codegen
```

- [ ] **Step 2: Run `merge-schema`**

```bash
/tmp/codegen merge-schema \
  --introspection-json-path cmd/codegen/schematool/testdata/single_object/schema.json \
  --module-types-path     cmd/codegen/schematool/testdata/single_object/module_types.json \
  > /tmp/merged.json
```

Expected: exit 0; `/tmp/merged.json` contains a schema with `Echo` present.

- [ ] **Step 3: Run `inspect-schema`**

```bash
/tmp/codegen inspect-schema list-types \
  --introspection-json-path /tmp/merged.json
```

Expected: JSON array including `"Echo"`.

```bash
/tmp/codegen inspect-schema has-type --name Echo \
  --introspection-json-path /tmp/merged.json
```

Expected: `true`, exit 0.

```bash
/tmp/codegen inspect-schema has-type --name DoesNotExist \
  --introspection-json-path /tmp/merged.json
```

Expected: `false`, exit 1.

- [ ] **Step 4: Clean up**

```bash
rm -f /tmp/codegen /tmp/merged.json
```

### Task 1.12: Refresh the patch and sanity-check

- [ ] **Step 1: Stage all created/modified files**

```bash
git add cmd/codegen/schematool/ cmd/codegen/inspect_schema.go cmd/codegen/merge_schema.go cmd/codegen/main.go
```

- [ ] **Step 2: Refresh the patch**

```bash
stg refresh
```

- [ ] **Step 3: Verify patch shape**

```bash
stg show schematool-foundation | head -50
```

Expected: shows created files + the `main.go` diff.

- [ ] **Step 4: Run full cmd/codegen test suite**

Run: `go test ./cmd/codegen/...`
Expected: all PASS.

---

## Commit 2 — AST-based Go source analyzer

**Patch name:** `astscan-go-typedefs`

**Intent:** introduce `cmd/codegen/generator/go/astscan/`, a self-contained AST walker that produces `schematool.ModuleTypes` from a module source directory. No caller yet — the Go codegen still uses `packages.Load`.

### Task 2.1: Create the patch

- [ ] **Step 1**

```bash
stg new -m "cmd/codegen/generator/go: add AST-based module type scanner

Introduce astscan, a go/parser + go/ast walker that extracts a
module's declared types (structs, interfaces, typed-const enums)
and functions into the language-agnostic schematool.ModuleTypes
shape. Type references are resolved against imports and the
introspection JSON; unresolved external types produce a clean
error.

Not wired into generate-module yet.

Signed-off-by: Yves Brissaud <yves@dagger.io>" astscan-go-typedefs
```

### Task 2.2: Scaffold the package

**Files:**

- Create: `cmd/codegen/generator/go/astscan/scanner.go`

- [ ] **Step 1: Skeleton**

```go
// Package astscan extracts a Dagger module's declared types from
// Go source code using go/parser + go/ast. It resolves type
// references against the module's imports and the supplied
// introspection schema; it does NOT invoke `go build` or
// packages.Load.
//
// See hack/designs/no-codegen-at-runtime-moduletypes.md.
package astscan

import (
    "go/ast"
    "go/parser"
    "go/token"
    "os"
    "path/filepath"

    "github.com/dagger/dagger/cmd/codegen/introspection"
    "github.com/dagger/dagger/cmd/codegen/schematool"
)

// Scan parses all .go files in dir (non-recursive, skipping _test.go)
// and returns the declared module types.
//
// schema is used to resolve references like dagger.Container →
// an introspection.Type in the schema. moduleName is the module's
// name (as it will appear in the emitted ModuleTypes).
func Scan(dir, moduleName string, schema *introspection.Schema) (*schematool.ModuleTypes, error) {
    fset := token.NewFileSet()
    pkg, err := parsePackage(fset, dir)
    if err != nil {
        return nil, err
    }
    return (&scanner{
        fset:       fset,
        pkg:        pkg,
        schema:     schema,
        moduleName: moduleName,
    }).run()
}

type scanner struct {
    fset       *token.FileSet
    pkg        *ast.Package
    schema     *introspection.Schema
    moduleName string
    imports    map[string]string // local alias → import path, per-file scope
}

func parsePackage(fset *token.FileSet, dir string) (*ast.Package, error) {
    entries, err := os.ReadDir(dir)
    if err != nil {
        return nil, err
    }
    pkgs := map[string]*ast.Package{}
    for _, e := range entries {
        if e.IsDir() || filepath.Ext(e.Name()) != ".go" {
            continue
        }
        if len(e.Name()) > 8 && e.Name()[len(e.Name())-8:] == "_test.go" {
            continue
        }
        path := filepath.Join(dir, e.Name())
        file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
        if err != nil {
            return nil, err
        }
        pkg, ok := pkgs[file.Name.Name]
        if !ok {
            pkg = &ast.Package{Name: file.Name.Name, Files: map[string]*ast.File{}}
            pkgs[file.Name.Name] = pkg
        }
        pkg.Files[path] = file
    }
    // Prefer `main` if present (Dagger modules use `package main`).
    if p, ok := pkgs["main"]; ok {
        return p, nil
    }
    for _, p := range pkgs {
        return p, nil
    }
    return &ast.Package{Name: "empty", Files: map[string]*ast.File{}}, nil
}

func (s *scanner) run() (*schematool.ModuleTypes, error) {
    out := &schematool.ModuleTypes{Name: s.moduleName}
    // Future: walk s.pkg.Files, build struct/interface/enum defs.
    return out, nil
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./cmd/codegen/generator/go/astscan/`
Expected: clean build.

### Task 2.3: Test — empty package returns empty ModuleTypes

**Files:**

- Create: `cmd/codegen/generator/go/astscan/testdata/empty/main.go`
- Create: `cmd/codegen/generator/go/astscan/scanner_test.go`

- [ ] **Step 1: Fixture**

`cmd/codegen/generator/go/astscan/testdata/empty/main.go`:

```go
package main

func main() {}
```

- [ ] **Step 2: Test**

```go
package astscan_test

import (
    "encoding/json"
    "os"
    "path/filepath"
    "testing"

    "github.com/dagger/dagger/cmd/codegen/generator/go/astscan"
    "github.com/dagger/dagger/cmd/codegen/introspection"
)

func TestScan_Empty(t *testing.T) {
    schema := loadSchema(t)
    mt, err := astscan.Scan(filepath.Join("testdata", "empty"), "empty", schema)
    if err != nil {
        t.Fatalf("scan: %v", err)
    }
    if len(mt.Objects) != 0 || len(mt.Interfaces) != 0 || len(mt.Enums) != 0 {
        t.Errorf("expected empty ModuleTypes, got %+v", mt)
    }
}

func loadSchema(t *testing.T) *introspection.Schema {
    t.Helper()
    data, err := os.ReadFile(filepath.Join("testdata", "schema.json"))
    if err != nil {
        t.Fatalf("read schema: %v", err)
    }
    var resp introspection.Response
    if err := json.Unmarshal(data, &resp); err != nil {
        t.Fatalf("unmarshal schema: %v", err)
    }
    return resp.Schema
}
```

- [ ] **Step 3: Minimal schema fixture**

`cmd/codegen/generator/go/astscan/testdata/schema.json` — copy from `cmd/codegen/schematool/testdata/single_object/schema.json`.

- [ ] **Step 4: Run**

Run: `go test ./cmd/codegen/generator/go/astscan/ -run TestScan_Empty -v`
Expected: PASS.

### Task 2.4: Test — single struct with method

**Files:**

- Create: `cmd/codegen/generator/go/astscan/testdata/single_struct/main.go`
- Create: `cmd/codegen/generator/go/astscan/testdata/single_struct/expected.json`
- Modify: `cmd/codegen/generator/go/astscan/scanner_test.go`

- [ ] **Step 1: Fixture**

`testdata/single_struct/main.go`:

```go
package main

import "context"

type Echo struct{}

// Say returns the greeting.
func (e *Echo) Say(ctx context.Context, msg string) string {
    return "hello " + msg
}
```

`testdata/single_struct/expected.json`:

```json
{
  "name": "echo",
  "objects": [
    {
      "name": "Echo",
      "functions": [
        {
          "name": "say",
          "description": "Say returns the greeting.",
          "args": [
            { "name": "msg", "type": { "kind": "NON_NULL", "ofType": { "kind": "SCALAR", "name": "String" } } }
          ],
          "returnType": { "kind": "NON_NULL", "ofType": { "kind": "SCALAR", "name": "String" } }
        }
      ]
    }
  ]
}
```

- [ ] **Step 2: Table-driven test**

Replace `TestScan_Empty` with a table test:

```go
func TestScan(t *testing.T) {
    cases := []struct {
        name       string
        moduleName string
    }{
        {"empty", "empty"},
        {"single_struct", "echo"},
    }
    schema := loadSchema(t)
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            got, err := astscan.Scan(filepath.Join("testdata", tc.name), tc.moduleName, schema)
            if err != nil {
                t.Fatalf("scan: %v", err)
            }
            expectedPath := filepath.Join("testdata", tc.name, "expected.json")
            expectedBytes, err := os.ReadFile(expectedPath)
            if err != nil {
                // empty case may not have expected.json
                if len(got.Objects)+len(got.Interfaces)+len(got.Enums) == 0 {
                    return
                }
                t.Fatalf("read expected: %v", err)
            }
            gotBytes, _ := json.Marshal(got)
            assertJSONEqual(t, gotBytes, expectedBytes)
        })
    }
}
```

Add `assertJSONEqual` (same impl as in `schematool_test.go`).

- [ ] **Step 3: Run and verify failure**

Run: `go test ./cmd/codegen/generator/go/astscan/ -run TestScan/single_struct -v`
Expected: FAIL — scanner returns empty, expected non-empty.

### Task 2.5: Implement struct and method extraction

**Files:**

- Modify: `cmd/codegen/generator/go/astscan/scanner.go`
- Create: `cmd/codegen/generator/go/astscan/resolve.go`

- [ ] **Step 1: Implement type resolution**

`resolve.go`:

```go
package astscan

import (
    "fmt"
    "go/ast"
    "strings"

    "github.com/dagger/dagger/cmd/codegen/introspection"
    "github.com/dagger/dagger/cmd/codegen/schematool"
)

const daggerImportPath = "dagger.io/dagger"

// resolveType converts a Go AST type expression into a schematool.TypeRef.
// imports maps local aliases (package names) to their import paths.
// sameModuleTypes is the set of names declared in the module itself —
// these resolve to OBJECT kind with an empty schema lookup.
func (s *scanner) resolveType(expr ast.Expr, imports map[string]string, sameModuleTypes map[string]string) (*schematool.TypeRef, error) {
    switch t := expr.(type) {
    case *ast.StarExpr:
        inner, err := s.resolveType(t.X, imports, sameModuleTypes)
        if err != nil {
            return nil, err
        }
        return inner, nil // pointer makes optional; we ignore pointer-ness here
    case *ast.ArrayType:
        inner, err := s.resolveType(t.Elt, imports, sameModuleTypes)
        if err != nil {
            return nil, err
        }
        return &schematool.TypeRef{
            Kind: "NON_NULL",
            OfType: &schematool.TypeRef{
                Kind:   "LIST",
                OfType: nonNull(inner),
            },
        }, nil
    case *ast.Ident:
        return s.resolveIdent(t, sameModuleTypes)
    case *ast.SelectorExpr:
        pkg, ok := t.X.(*ast.Ident)
        if !ok {
            return nil, fmt.Errorf("unsupported selector: %T", t.X)
        }
        return s.resolveSelector(pkg.Name, t.Sel.Name, imports)
    }
    return nil, fmt.Errorf("unsupported type expression: %T", expr)
}

func (s *scanner) resolveIdent(id *ast.Ident, sameModuleTypes map[string]string) (*schematool.TypeRef, error) {
    if kind, ok := builtinTypes[id.Name]; ok {
        return nonNull(&schematool.TypeRef{Kind: kind, Name: scalarName(id.Name)}), nil
    }
    if kind, ok := sameModuleTypes[id.Name]; ok {
        return nonNull(&schematool.TypeRef{Kind: kind, Name: id.Name}), nil
    }
    return nil, fmt.Errorf("unresolved identifier %q", id.Name)
}

func (s *scanner) resolveSelector(pkgAlias, typeName string, imports map[string]string) (*schematool.TypeRef, error) {
    path, ok := imports[pkgAlias]
    if !ok {
        return nil, fmt.Errorf("unknown import alias %q", pkgAlias)
    }
    if path == "context" && typeName == "Context" {
        // Special-cased: contexts are a sentinel arg in Dagger; generator drops them.
        return nil, errContextArg
    }
    if path == daggerImportPath {
        t := s.schema.Types.Get(typeName)
        if t == nil {
            return nil, fmt.Errorf("type %s.%s not found in introspection schema", pkgAlias, typeName)
        }
        return nonNull(&schematool.TypeRef{Kind: string(t.Kind), Name: typeName}), nil
    }
    return nil, fmt.Errorf("unsupported external type %s.%s (import %q)", pkgAlias, typeName, path)
}

var errContextArg = fmt.Errorf("context.Context")

// builtinTypes maps Go primitives to GraphQL SCALAR kinds.
var builtinTypes = map[string]string{
    "string":  "SCALAR",
    "int":     "SCALAR",
    "int32":   "SCALAR",
    "int64":   "SCALAR",
    "float32": "SCALAR",
    "float64": "SCALAR",
    "bool":    "SCALAR",
}

func scalarName(goName string) string {
    switch goName {
    case "string":
        return "String"
    case "int", "int32", "int64":
        return "Int"
    case "float32", "float64":
        return "Float"
    case "bool":
        return "Boolean"
    }
    return goName
}

func nonNull(t *schematool.TypeRef) *schematool.TypeRef {
    if t == nil {
        return nil
    }
    if strings.EqualFold(t.Kind, "NON_NULL") {
        return t
    }
    return &schematool.TypeRef{Kind: "NON_NULL", OfType: t}
}
```

- [ ] **Step 2: Implement struct + method walking in scanner.go**

Replace `run()` and add helpers:

```go
func (s *scanner) run() (*schematool.ModuleTypes, error) {
    out := &schematool.ModuleTypes{Name: s.moduleName}

    // Two-pass: first collect type names (for same-module lookup),
    // then extract details.
    sameModule := map[string]string{}
    for _, f := range s.pkg.Files {
        for _, decl := range f.Decls {
            gd, ok := decl.(*ast.GenDecl)
            if !ok || gd.Tok != token.TYPE {
                continue
            }
            for _, spec := range gd.Specs {
                ts := spec.(*ast.TypeSpec)
                switch ts.Type.(type) {
                case *ast.StructType:
                    sameModule[ts.Name.Name] = "OBJECT"
                case *ast.InterfaceType:
                    sameModule[ts.Name.Name] = "INTERFACE"
                }
            }
        }
    }

    // Walk files.
    methods := map[string][]*ast.FuncDecl{} // receiver name → methods
    for _, f := range s.pkg.Files {
        imports := collectImports(f)
        for _, decl := range f.Decls {
            switch d := decl.(type) {
            case *ast.GenDecl:
                if err := s.walkGenDecl(out, d, imports, sameModule); err != nil {
                    return nil, err
                }
            case *ast.FuncDecl:
                if d.Recv == nil || len(d.Recv.List) == 0 {
                    continue
                }
                recvName := recvTypeName(d.Recv.List[0].Type)
                if recvName == "" {
                    continue
                }
                if _, isSameMod := sameModule[recvName]; !isSameMod {
                    continue
                }
                methods[recvName] = append(methods[recvName], d)
            }
        }
    }

    // Attach methods to the appropriate object/interface entries in out.
    for i, obj := range out.Objects {
        for _, m := range methods[obj.Name] {
            fn, err := s.walkMethod(m, collectImports(findFile(s.pkg, m)), sameModule)
            if err != nil {
                return nil, err
            }
            if fn != nil {
                out.Objects[i].Functions = append(out.Objects[i].Functions, *fn)
            }
        }
    }
    return out, nil
}

func findFile(pkg *ast.Package, decl *ast.FuncDecl) *ast.File {
    for _, f := range pkg.Files {
        for _, d := range f.Decls {
            if d == decl {
                return f
            }
        }
    }
    return nil
}

func collectImports(f *ast.File) map[string]string {
    imports := map[string]string{}
    for _, imp := range f.Imports {
        path := trimQuotes(imp.Path.Value)
        alias := ""
        if imp.Name != nil {
            alias = imp.Name.Name
        } else {
            parts := strings.Split(path, "/")
            alias = parts[len(parts)-1]
        }
        imports[alias] = path
    }
    return imports
}

func trimQuotes(s string) string {
    if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
        return s[1 : len(s)-1]
    }
    return s
}

func recvTypeName(expr ast.Expr) string {
    switch t := expr.(type) {
    case *ast.StarExpr:
        return recvTypeName(t.X)
    case *ast.Ident:
        return t.Name
    }
    return ""
}

func (s *scanner) walkGenDecl(out *schematool.ModuleTypes, gd *ast.GenDecl, imports map[string]string, sameModule map[string]string) error {
    if gd.Tok != token.TYPE {
        return nil
    }
    for _, spec := range gd.Specs {
        ts := spec.(*ast.TypeSpec)
        switch st := ts.Type.(type) {
        case *ast.StructType:
            out.Objects = append(out.Objects, schematool.ObjectDef{
                Name:        ts.Name.Name,
                Description: strings.TrimSpace(gd.Doc.Text()),
            })
            _ = st // fields not emitted in this commit
        case *ast.InterfaceType:
            out.Interfaces = append(out.Interfaces, schematool.InterfaceDef{
                Name:        ts.Name.Name,
                Description: strings.TrimSpace(gd.Doc.Text()),
            })
            _ = st // interface methods handled later
        }
    }
    return nil
}

func (s *scanner) walkMethod(fd *ast.FuncDecl, imports map[string]string, sameModule map[string]string) (*schematool.Function, error) {
    if !fd.Name.IsExported() {
        return nil, nil
    }
    fn := &schematool.Function{
        Name:        lowerFirst(fd.Name.Name),
        Description: strings.TrimSpace(fd.Doc.Text()),
    }

    if fd.Type.Params != nil {
        for _, field := range fd.Type.Params.List {
            tref, err := s.resolveType(field.Type, imports, sameModule)
            if err == errContextArg {
                continue
            }
            if err != nil {
                return nil, fmt.Errorf("method %s param: %w", fd.Name.Name, err)
            }
            for _, name := range field.Names {
                fn.Args = append(fn.Args, schematool.FuncArg{
                    Name:    name.Name,
                    TypeRef: tref,
                })
            }
        }
    }

    if fd.Type.Results == nil || len(fd.Type.Results.List) == 0 {
        return nil, fmt.Errorf("method %s has no return type", fd.Name.Name)
    }
    // Use first non-error return as payload.
    for _, res := range fd.Type.Results.List {
        tref, err := s.resolveType(res.Type, imports, sameModule)
        if err != nil {
            // `error` return is fine; skip.
            if ident, ok := res.Type.(*ast.Ident); ok && ident.Name == "error" {
                continue
            }
            return nil, fmt.Errorf("method %s return: %w", fd.Name.Name, err)
        }
        fn.ReturnType = tref
        break
    }
    return fn, nil
}

func lowerFirst(s string) string {
    if s == "" {
        return s
    }
    return strings.ToLower(s[:1]) + s[1:]
}
```

- [ ] **Step 3: Run**

Run: `go test ./cmd/codegen/generator/go/astscan/ -run TestScan/single_struct -v`
Expected: PASS.

### Task 2.6: Interface fixture

**Files:**

- Create: `cmd/codegen/generator/go/astscan/testdata/interface/main.go`
- Create: `cmd/codegen/generator/go/astscan/testdata/interface/expected.json`

- [ ] **Step 1: Fixture**

```go
package main

// Animal is a thing that makes sound.
type Animal interface {
    // Sound produces a noise.
    Sound() string
}
```

- [ ] **Step 2: Expected**

Produces a `schematool.ModuleTypes` with one interface entry. Write the minimal expected JSON matching what your implementation emits.

- [ ] **Step 3: Extend the scanner to walk interface methods**

In `walkGenDecl`, when encountering `*ast.InterfaceType`, iterate `st.Methods.List`, extract each `*ast.FuncType`, and append to the appropriate `InterfaceDef.Functions`. Use the same param/return resolution helper (extract from `walkMethod` into a shared `walkFuncType`).

- [ ] **Step 4: Add `"interface"` to the test cases and run**

Run: `go test ./cmd/codegen/generator/go/astscan/ -run TestScan/interface -v`
Expected: PASS.

### Task 2.7: Enum fixture (typed string constants)

**Files:**

- Create: `cmd/codegen/generator/go/astscan/testdata/enum/main.go`
- Create: `cmd/codegen/generator/go/astscan/testdata/enum/expected.json`

- [ ] **Step 1: Fixture**

```go
package main

// Status represents a state.
type Status string

const (
    // StatusPending is the initial state.
    StatusPending Status = "PENDING"
    // StatusActive is the running state.
    StatusActive Status = "ACTIVE"
)
```

- [ ] **Step 2: Expected**

A `ModuleTypes` with one `Enum` entry `{Name:"Status", Values:[{Name:"PENDING"}, {Name:"ACTIVE"}]}`.

- [ ] **Step 3: Implement enum detection in walkGenDecl**

When a `TypeSpec` is `*ast.Ident{Name:"string"}` (the typed-string pattern), track the named type as a candidate enum. Then walk all `*ast.GenDecl{Tok: token.CONST}` and group constants whose declared type matches a candidate.

- [ ] **Step 4: Run**

Run: `go test ./cmd/codegen/generator/go/astscan/ -run TestScan/enum -v`
Expected: PASS.

### Task 2.8: Error cases

**Files:**

- Create: `cmd/codegen/generator/go/astscan/testdata/unresolved_type/main.go`
- Modify: `cmd/codegen/generator/go/astscan/scanner_test.go`

- [ ] **Step 1: Fixture**

```go
package main

import "example.com/foreign"

type Echo struct{}

func (e *Echo) Use(x foreign.Thing) string { return "" }
```

- [ ] **Step 2: Test the error**

```go
func TestScan_UnresolvedType(t *testing.T) {
    schema := loadSchema(t)
    _, err := astscan.Scan(filepath.Join("testdata", "unresolved_type"), "echo", schema)
    if err == nil {
        t.Fatal("expected error, got nil")
    }
    if !strings.Contains(err.Error(), "unsupported external type") {
        t.Errorf("unexpected error: %v", err)
    }
}
```

Add `"strings"` to imports.

- [ ] **Step 3: Run**

Run: `go test ./cmd/codegen/generator/go/astscan/ -run TestScan_UnresolvedType -v`
Expected: PASS.

### Task 2.9: Refresh and full test

- [ ] **Step 1: Stage**

```bash
git add cmd/codegen/generator/go/astscan/
```

- [ ] **Step 2: Refresh**

```bash
stg refresh
```

- [ ] **Step 3: Run full cmd/codegen test suite**

Run: `go test ./cmd/codegen/...`
Expected: all PASS.

---

## Commit 3 — Rewire `generate-module` to use astscan + schematool

**Patch name:** `generate-module-ast-pilot`

**Intent:** replace the `packages.Load`-driven typedef pass in `generate-module` with the AST scanner + in-process `schematool.Merge`. Legacy path preserved under `//go:build legacy_typedefs` and `--legacy-typedefs` flag.

### Task 3.1: Create the patch

```bash
stg new -m "cmd/codegen/generator/go: use astscan + schematool in generate-module

Fold Phase-1 (source analysis) and Phase-2 (schema merge) into
generate-module so Go modules no longer need a separate
generate-typedefs pass. The AST-based scanner replaces
packages.Load; schema merging happens in-process via the
schematool library.

Legacy packages.Load path retained behind //go:build
legacy_typedefs for parity comparison. Drop in PR 2.

Signed-off-by: Yves Brissaud <yves@dagger.io>" generate-module-ast-pilot
```

### Task 3.2: Move legacy generator files behind a build tag

**Files:**

- Modify: `cmd/codegen/generator/go/generate_module.go` → copy current content to `generate_module_legacy.go`, add build tag
- Modify: `cmd/codegen/generator/go/generate_typedefs.go` → rename to `generate_typedefs_legacy.go`, add build tag

- [ ] **Step 1: Copy current `generate_module.go` to `generate_module_legacy.go`**

```bash
cp cmd/codegen/generator/go/generate_module.go cmd/codegen/generator/go/generate_module_legacy.go
```

- [ ] **Step 2: Prepend a build tag to the legacy file**

Insert at line 1 of `generate_module_legacy.go`:

```go
//go:build legacy_typedefs
// +build legacy_typedefs

```

- [ ] **Step 3: Rename `generate_typedefs.go`**

```bash
git mv cmd/codegen/generator/go/generate_typedefs.go cmd/codegen/generator/go/generate_typedefs_legacy.go
```

- [ ] **Step 4: Prepend a build tag to the renamed file**

Same build tag at line 1.

- [ ] **Step 5: Prepend a `//go:build !legacy_typedefs` tag to the NEW `generate_module.go`**

We'll overwrite `generate_module.go` in Task 3.3 — for now just put the build tag at line 1 with the existing content to verify the tag split compiles.

- [ ] **Step 6: Verify both tag settings compile**

```bash
go build ./cmd/codegen/...
go build -tags legacy_typedefs ./cmd/codegen/...
```

Both expected: clean.

- [ ] **Step 7: Run default-tag tests**

Run: `go test ./cmd/codegen/generator/go/...`
Expected: all PASS.

### Task 3.3: Rewrite `generate_module.go` to use astscan + schematool

**Files:**

- Modify: `cmd/codegen/generator/go/generate_module.go` (completely replaced)

- [ ] **Step 1: Replace the file**

The new `GenerateModule` runs:

1. `astscan.Scan(moduleSourcePath, moduleName, schema)` → `ModuleTypes`.
2. If self-calls enabled (see Step 2 for how to read the flag): `schematool.Merge(schema, modTypes)`.
3. Proceed with the rest of existing logic (bootstrap package, generate dagger.gen.go from the *now-merged* schema).

Full file content:

```go
//go:build !legacy_typedefs
// +build !legacy_typedefs

package gogenerator

import (
    "context"
    "fmt"
    "io/fs"
    "os"
    "path/filepath"

    "github.com/dagger/dagger/cmd/codegen/generator"
    "github.com/dagger/dagger/cmd/codegen/generator/go/astscan"
    "github.com/dagger/dagger/cmd/codegen/introspection"
    "github.com/dagger/dagger/cmd/codegen/schematool"
    "github.com/dschmidt/go-layerfs"
    "github.com/psanford/memfs"
)

func (g *GoGenerator) GenerateModule(ctx context.Context, schema *introspection.Schema, schemaVersion string) (*generator.GeneratedState, error) {
    if g.Config.ModuleConfig == nil {
        return nil, fmt.Errorf("generateModule is called but module config is missing")
    }

    moduleConfig := g.Config.ModuleConfig
    generator.SetSchema(schema)

    outDir := filepath.Clean(moduleConfig.ModuleSourcePath)
    mfs := memfs.New()
    layers := []fs.FS{mfs}
    genSt := &generator.GeneratedState{}

    pkgInfo, partial, err := g.bootstrapMod(mfs, genSt, false)
    if err != nil {
        return nil, fmt.Errorf("bootstrap package: %w", err)
    }
    genSt.Overlay = layerfs.New(layers...)

    if outDir != "." {
        _ = mfs.MkdirAll(outDir, 0700)
        sub, err := mfs.Sub(outDir)
        if err != nil {
            return nil, err
        }
        mfs = sub.(*memfs.FS)
    }

    initialGoFiles, err := filepath.Glob(filepath.Join(g.Config.OutputDir, outDir, "*.go"))
    if err != nil {
        return nil, fmt.Errorf("glob go files: %w", err)
    }

    genFile := filepath.Join(g.Config.OutputDir, outDir, ClientGenFile)
    if _, err := os.Stat(genFile); err != nil {
        pkgInfo.PackageName = "main"
        if err := generateCode(ctx, g.Config, schema, schemaVersion, mfs, pkgInfo, nil, nil, 0); err != nil {
            return nil, fmt.Errorf("generate code: %w", err)
        }
        partial = true
    }
    if len(initialGoFiles) == 0 {
        if err := mfs.WriteFile(StarterTemplateFile, []byte(baseModuleSource(pkgInfo, moduleConfig.ModuleName)), 0600); err != nil {
            return nil, err
        }
        partial = true
    }
    if partial {
        genSt.NeedRegenerate = true
        return genSt, nil
    }

    // Phase 1: AST scan the user's source.
    userSourceDir := filepath.Join(g.Config.OutputDir, outDir)
    modTypes, err := astscan.Scan(userSourceDir, moduleConfig.ModuleName, schema)
    if err != nil {
        return nil, fmt.Errorf("astscan: %w", err)
    }

    // Phase 2: merge if self-calls enabled. The SDK runtime decides by
    // setting ModuleConfig.SelfCalls; see Task 3.4 for wiring.
    if moduleConfig.SelfCalls {
        if err := schematool.Merge(schema, modTypes); err != nil {
            return nil, fmt.Errorf("schematool merge: %w", err)
        }
    }

    // Phase 3: generate bindings from the (possibly merged) schema.
    //
    // The existing generateCode path is reused: it walks whatever the
    // schema contains, which now includes self-types if self-calls on.
    // We still need `pkg` and `fset` for the second pass, to emit the
    // module's main.go stub and bindings; obtain them from the AST
    // scanner rather than packages.Load.
    pkg, fset, err := astscan.LoadPackage(userSourceDir)
    if err != nil {
        return nil, fmt.Errorf("load package %q: %w", outDir, err)
    }
    pkgInfo.PackageName = pkg.Name

    if err := generateCode(ctx, g.Config, schema, schemaVersion, mfs, pkgInfo, pkg, fset, 1); err != nil {
        return nil, fmt.Errorf("generate code: %w", err)
    }
    return genSt, nil
}
```

NOTE: `astscan.LoadPackage` returns `(*packages.Package, *token.FileSet, error)` — NO: the point of this PR is to drop `packages.Load`. Instead, `astscan.LoadPackage` should return `(*ast.Package, *token.FileSet, error)` and `generateCode`'s signature needs a matching change. See Step 3.

- [ ] **Step 2: Add `SelfCalls bool` to `ModuleGeneratorConfig`**

Modify `cmd/codegen/generator/config.go`, the `ModuleGeneratorConfig` struct: add `SelfCalls bool`. Also add `LegacyTypedefs bool` for completeness (ignored by non-legacy build).

- [ ] **Step 3: Switch `generateCode` and templates from `*packages.Package` to `*ast.Package`**

This is the single most invasive sub-task. The existing `generateCode`, `GoTypeDefsGenerator`, and templates consume `*packages.Package`. Options:

a) **Narrow the interface**: pass only what the templates use. Audit callsites; grep for `.Types()` / `.TypesInfo` / `packages.Package` usage.

b) **Feed the same struct through a shim**: keep `*packages.Package` as the template's expected input, but build a minimal one from AST (this recreates half of `packages.Load` and is not a good idea).

Go with (a). Minimum changes:

- In templates (`cmd/codegen/generator/go/templates/*.go`), `parseState` takes a package and fset; it uses `pkg.Types` for typechecked lookups. Replace those lookups with AST-level equivalents (the scanner already produces TypeDefs; templates can consume them directly instead of re-deriving from types.Info).
- Or: **swap templates** to consume `schematool.ModuleTypes` for the typedef-generation phase, and let the client-bindings phase (which needs only the schema, not user source) stay unchanged.

The practical path: **templates used for user-module bindings get `ModuleTypes` as input, not `*packages.Package`.** The client-bindings templates already don't need user source.

- [ ] **Step 4: Verify default build compiles**

```bash
go build ./cmd/codegen/...
```

Expected: clean.

- [ ] **Step 5: Verify legacy build still compiles**

```bash
go build -tags legacy_typedefs ./cmd/codegen/...
```

Expected: clean.

### Task 3.4: Add `--legacy-typedefs` flag + `--self-calls` flag

**Files:**

- Modify: `cmd/codegen/generate_module.go`
- Modify: `cmd/codegen/main.go`

- [ ] **Step 1: Add the flags to `generate_module.go`**

In `cmd/codegen/generate_module.go`'s `init()`:

```go
generateModuleCmd.Flags().BoolVar(&legacyTypedefs, "legacy-typedefs", false,
    "use the legacy packages.Load-based typedef extraction (rollback path)")
generateModuleCmd.Flags().BoolVar(&selfCalls, "self-calls", false,
    "include the module's own types in the generated bindings (requires SELF_CALLS experimental feature)")
```

Declare package-level `legacyTypedefs`, `selfCalls` bools at the top.

- [ ] **Step 2: Pipe into ModuleGeneratorConfig**

```go
moduleConfig.LegacyTypedefs = legacyTypedefs
moduleConfig.SelfCalls = selfCalls
```

- [ ] **Step 3: In legacy file, respect the flag**

`generate_module_legacy.go`'s `GenerateModule` runs only when built with the `legacy_typedefs` tag. To make the flag useful at runtime (without a rebuild), a small stub in the default build dispatches:

```go
// In generate_module.go (default build):
if moduleConfig.LegacyTypedefs {
    return nil, fmt.Errorf("--legacy-typedefs requires a binary built with -tags legacy_typedefs")
}
```

Rationale: keep runtime dispatch simple; users who need legacy rebuild once. This is a short-lived rollback affordance.

- [ ] **Step 4: Verify builds**

```bash
go build ./cmd/codegen/...
go build -tags legacy_typedefs ./cmd/codegen/...
```

Both expected: clean.

### Task 3.5: Update Go SDK runtime to pass --self-calls when appropriate

**Files:**

- Modify: `core/sdk/go_sdk.go` (around `baseWithCodegen`)

- [ ] **Step 1: Read current codegen arg list**

Already visible in earlier exploration: `codegenArgs` in `baseWithCodegen` is the `go codegen generate-module` command. We need to append `--self-calls` when the module source has `SELF_CALLS` experimental feature enabled.

- [ ] **Step 2: Add the arg conditionally**

In `baseWithCodegen`, after existing `codegenArgs` construction:

```go
if src.Self().SDK.ExperimentalFeatureEnabled(core.ModuleSourceExperimentalFeatureSelfCalls) {
    codegenArgs = append(codegenArgs, "--self-calls")
}
```

Use the constant already in `core/modulesource.go`.

- [ ] **Step 3: Verify build**

Run: `go build ./core/sdk/...`
Expected: clean.

### Task 3.6: Refresh and verify

- [ ] **Step 1: Stage all PR-3 changes**

```bash
git add cmd/codegen/ core/sdk/go_sdk.go
```

- [ ] **Step 2: Refresh the patch**

```bash
stg refresh
```

- [ ] **Step 3: Run cmd/codegen tests**

Run: `go test ./cmd/codegen/...`
Expected: all PASS (both schematool and astscan tests).

- [ ] **Step 4: Run core/sdk tests**

Run: `go test ./core/sdk/...`
Expected: all PASS.

---

## Commit 4 — Go SDK engine-side stops advertising moduleTypes

**Patch name:** `go-sdk-drop-moduletypes`

**Intent:** Go SDK's `AsModuleTypes` returns `nil, false` so the engine falls through to the `Runtime` + empty-function-name path. `ModuleTypes` method body on `*goSDK` is deleted (code under `core/sdk/go_sdk.go:227`).

### Task 4.1: Create the patch

```bash
stg new -m "core/sdk/go_sdk: stop advertising moduleTypes

Go SDK now handles type discovery entirely within generate-module
(AST scan + schematool merge). AsModuleTypes returns nil,false so
the engine falls through to the Runtime + empty-function-name
path at asModule time — the same path that existed before
moduleTypes was introduced.

The ModuleTypes interface and its engine-side dispatch remain in
place for module-SDKs (Python, TS, ...) which haven't migrated
yet. They will be deleted in the final cleanup PR once every SDK
has migrated.

Signed-off-by: Yves Brissaud <yves@dagger.io>" go-sdk-drop-moduletypes
```

### Task 4.2: Modify `core/sdk/go_sdk.go`

**Files:**

- Modify: `core/sdk/go_sdk.go`

- [ ] **Step 1: Change AsModuleTypes**

Replace:

```go
func (sdk *goSDK) AsModuleTypes() (core.ModuleTypes, bool) {
    return sdk, true
}
```

With:

```go
func (sdk *goSDK) AsModuleTypes() (core.ModuleTypes, bool) {
    // Go SDK handles type discovery entirely within generate-module
    // (AST scan + schematool merge). The engine falls through to the
    // Runtime + empty-function-name path at asModule time.
    return nil, false
}
```

- [ ] **Step 2: Delete the ModuleTypes method body**

Delete the entire `func (sdk *goSDK) ModuleTypes(...)` method (lines 227–382). Don't leave a stub — it no longer satisfies the interface anyway since we return `nil` from `AsModuleTypes`.

Also remove any now-unused imports (`"encoding/json"` if only this method used it; `"github.com/dagger/dagger/dagql/call"` likewise — check with `goimports` or a compile.)

- [ ] **Step 3: Verify build**

Run: `go build ./core/sdk/...`
Expected: clean.

- [ ] **Step 4: Run core/sdk tests**

Run: `go test ./core/sdk/...`
Expected: all PASS.

### Task 4.3: Refresh

- [ ] **Step 1: Stage**

```bash
git add core/sdk/go_sdk.go
```

- [ ] **Step 2: Refresh**

```bash
stg refresh
```

---

## Commit 5 — Integration tests: self-calls + parity on the new path

**Patch name:** `go-sdk-new-path-integration-tests`

**Intent:** exercise the new Go SDK codegen path end-to-end, including self-calls. Add a parity test comparing `dagger.gen.go` output between legacy and new paths.

### Task 5.1: Create the patch

```bash
stg new -m "core/integration: test Go SDK on the new generate-module path

Cover self-calls and non-self-calls modules through the AST +
schematool flow. Add a parity comparison against the legacy
packages.Load path when built with -tags legacy_typedefs to guard
against regressions during soak.

Signed-off-by: Yves Brissaud <yves@dagger.io>" go-sdk-new-path-integration-tests
```

### Task 5.2: Ensure existing tests pass on the new path

**Files:** none modified.

- [ ] **Step 1: Run the full Go-SDK module test suite**

Run: `go test -count=1 -run TestModule ./core/integration/ -timeout 30m`

Note: this is a long-running suite (integration tests launch real engines). Expected: every Go-SDK subtest PASS. If anything fails, diagnose before proceeding — this is the gate for the whole PR.

- [ ] **Step 2: Specifically run the self-calls suite**

Run: `go test -count=1 -run TestSelfCalls ./core/integration/ -timeout 15m`

Expected: PASS. This is the most important single gate for the design.

### Task 5.3: Add parity test (new vs legacy)

**Files:**

- Modify: `core/integration/module_test.go`

- [ ] **Step 1: Write the test**

Append a test that for a representative Go module:

1. Runs `dagger develop` (or equivalent) on the module source.
2. Captures the generated `dagger.gen.go`.
3. Rebuilds `cmd/codegen` with `-tags legacy_typedefs`, re-runs, captures again.
4. `bytes.Equal` the two, or produces a unified diff on mismatch.

The precise test harness depends on the existing integration-test helpers — use the same `modInit` / `modDevelop` patterns other tests use in the file.

Example skeleton (fill in using the same helpers the rest of `module_test.go` uses):

```go
func (ModuleSuite) TestGoCodegenPhase1Parity(ctx context.Context, t *testctx.T) {
    t.Skip("rebuild-with-tag harness not yet implemented; tracked in PR 2")
    // TODO(yves, PR 2): wire a two-build compare once the dev harness
    // exposes a `go build -tags legacy_typedefs` helper for cmd/codegen.
}
```

Leave the test skipped in PR 1 — the dev harness isn't in place yet. Mark the skip with a TODO referencing PR 2. This keeps the PR focused.

- [ ] **Step 2: Verify it compiles and skips cleanly**

Run: `go test -count=1 -run TestModuleSuite/TestGoCodegenPhase1Parity ./core/integration/`

Expected: PASS (skipped).

### Task 5.4: Add self-calls-on-new-path smoke test

**Files:**

- Modify: `core/integration/module_test.go`

- [ ] **Step 1: Review existing self-calls test**

Find `TestSelfCalls` (around line 6397). It already exercises self-calls. With Go SDK on the new path, the existing test is already the smoke test — no new test is needed.

- [ ] **Step 2: Add a targeted assertion**

Add (or inline into the existing test) a check confirming `dagger.gen.go` contains methods for the module's own types when self-calls is on. This asserts Phase 2 actually merged self-types into the schema.

Example (adjust to match the existing test's idioms):

```go
// After dagger develop, inspect dagger.gen.go for a symbol that
// only exists when the schema has been merged with self-types.
gen := modGen.File("dagger.gen.go")
contents, err := gen.Contents(ctx)
require.NoError(t, err)
require.Contains(t, contents, "func (m *Echo) Say(", "self-call binding must be generated")
```

- [ ] **Step 3: Run**

Run: `go test -count=1 -run TestSelfCalls ./core/integration/ -timeout 15m`

Expected: PASS.

### Task 5.5: Refresh the patch and run the full suite

- [ ] **Step 1: Stage**

```bash
git add core/integration/
```

- [ ] **Step 2: Refresh**

```bash
stg refresh
```

- [ ] **Step 3: Run the full integration suite one more time**

```bash
go test -count=1 ./core/integration/ -timeout 45m
```

Expected: all PASS. If not, diagnose before declaring PR 1 complete.

### Task 5.6: Verify the stack

- [ ] **Step 1: List patches**

```bash
stg series
```

Expected output:

```
+ no-codegen-at-runtime-design
+ schematool-foundation
+ astscan-go-typedefs
+ generate-module-ast-pilot
+ go-sdk-drop-moduletypes
> go-sdk-new-path-integration-tests
```

- [ ] **Step 2: Pushability check**

```bash
stg sync --ref-branch origin/main
```

Expected: no conflicts against origin/main at the time of this PR.

- [ ] **Step 3: Confirm no uncommitted changes**

```bash
git status
```

Expected: `working tree clean` (all changes are refreshed into their patches).

---

## Self-Review

1. **Spec coverage:**
   - schema subcommands (inspect-schema, merge-schema) → Commit 1 ✓
   - AST-only source analyzer → Commit 2 ✓
   - Unified generate-module → Commit 3 ✓
   - Legacy path retained + flag → Commit 3 ✓
   - Go SDK engine side stops advertising moduleTypes → Commit 4 ✓
   - Integration tests for self-calls + non-self-calls → Commit 5 ✓
   - Parity test: deferred to PR 2 with a skip stub in Commit 5 (spec section "Testing strategy" mentions parity; this is an intentional partial pending harness)
   - Module-SDKs untouched → no-op, satisfied by the Go-SDK-only scope ✓

2. **Placeholder scan:** one deferred item is Task 5.3 (parity harness skip). Every other task has concrete code and commands.

3. **Type consistency:**
   - `schematool.ModuleTypes` struct defined in Task 1.2; used in Task 1.10, Task 2.2, Task 3.3 — same name, same fields.
   - `astscan.Scan` signature defined in Task 2.2; called in Task 3.3 — matches.
   - `ModuleGeneratorConfig.SelfCalls` added in Task 3.3 Step 2; consumed in Task 3.4 Step 2 — matches.

4. **Scope:** single PR's worth of work. Does not cross into PR 2 (legacy deletion) or PR 3 (first module-SDK migration).

Identified gap during self-review — noted and fixed inline: Task 3.3 originally suggested `astscan.LoadPackage` returning `*packages.Package`, which contradicts the whole point of dropping `packages.Load`. The step-3 note corrects this by pushing template rework into the same task.

## Execution Handoff

Plan complete and saved to `hack/designs/no-codegen-at-runtime-pr1-plan.md`. Two execution options:

**1. Subagent-Driven (recommended)** — dispatch a fresh subagent per task, review between tasks, fast iteration. Good for a plan this size where individual tasks are independent-ish.

**2. Inline Execution** — execute tasks in this session using executing-plans, batch with checkpoints for review.

Which approach?
