package astscan_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/cmd/codegen/generator/go/astscan"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"golang.org/x/tools/go/packages"
)

// BenchmarkScan times the in-process work astscan performs on a
// representative module — single-pass, no go-toolchain invocation.
func BenchmarkScan(b *testing.B) {
	schema := loadBenchSchema(b)
	dir := filepath.Join("testdata", "module_local_dagger")
	for b.Loop() {
		if _, err := astscan.Scan(dir, "my-module", schema); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPackagesLoad times what the previous codegen path did to
// achieve roughly the same goal: packages.Load on a fully-resolved
// user package, with the same Mode flags the old generate-typedefs
// configured. The fixture is a self-contained module on disk so the
// loader doesn't go to the network.
//
// This is the wall-clock work the codegen container saved per call
// once the legacy moduleTypes path was removed; per-codegen cycles
// also avoided one go-get and one extra codegen pass on top of this.
func BenchmarkPackagesLoad(b *testing.B) {
	dir := setupPackagesLoadFixture(b)
	cfg := &packages.Config{
		Dir:   dir,
		Tests: false,
		Mode:  packages.NeedName | packages.NeedTypes | packages.NeedSyntax | packages.NeedModule,
		ParseFile: func(fset *token.FileSet, filename string, src []byte) (*ast.File, error) {
			astFile, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
			if err != nil {
				return nil, err
			}
			for _, decl := range astFile.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok {
					fn.Body = nil
				}
			}
			return astFile, nil
		},
	}
	for b.Loop() {
		if _, err := packages.Load(cfg, "."); err != nil {
			b.Fatal(err)
		}
	}
}

// loadBenchSchema reads testdata/schema.json the same way TestScan
// does. Kept separate from the test helper to avoid coupling the
// benchmark to the test package's internals.
func loadBenchSchema(tb testing.TB) *introspection.Schema {
	tb.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "schema.json"))
	if err != nil {
		tb.Fatalf("read schema: %v", err)
	}
	var resp introspection.Response
	if err := json.Unmarshal(data, &resp); err != nil {
		tb.Fatalf("unmarshal schema: %v", err)
	}
	return resp.Schema
}

// setupPackagesLoadFixture writes a small self-contained Go module
// to a temp dir so packages.Load can succeed without network. The
// shape mirrors a Dagger module: package main, a stubbed
// internal/dagger that mimics the real generated bindings, and one
// or two methods on a user struct.
func setupPackagesLoadFixture(tb testing.TB) string {
	tb.Helper()
	dir := tb.TempDir()

	write := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			tb.Fatal(err)
		}
	}

	write("go.mod", "module dagger/bench\n\ngo 1.25\n")
	write("internal/dagger/dagger.gen.go", `package dagger

type Directory struct{}
type Container struct{}
func (d *Directory) ID() string { return "" }
func (c *Container) From(addr string) *Container { return nil }
func (c *Container) WithExec(args []string) *Container { return nil }
`)
	write("dag.go", `package main

import "dagger/bench/internal/dagger"

var dag *daggerClient
type daggerClient struct{}
func (d *daggerClient) Container() *dagger.Container { return nil }
`)
	write("main.go", `package main

import (
	"context"
	"dagger/bench/internal/dagger"
)

type Bench struct{}

type Greeting struct {
	Message string
}

type Status string

const (
	StatusActive  Status = "ACTIVE"
	StatusPending Status = "PENDING"
)

func (b *Bench) Greet(ctx context.Context, name string) *Greeting {
	return &Greeting{Message: "hello " + name}
}

func (b *Bench) Build(ctx context.Context, src *dagger.Directory, target string) (*dagger.Container, error) {
	return dag.Container().From("alpine"), nil
}

func (b *Bench) Status(s Status) string {
	return string(s)
}
`)

	return dir
}
