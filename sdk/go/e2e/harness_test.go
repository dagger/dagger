// The outer harness uses the released Dagger Go client declared by this
// module. Development client code is mounted into isolated inner test modules.
//
//go:test:include ../../../analytics/**
//go:test:include ../../../auth/**
//go:test:include ../../../cmd/**
//go:test:include ../../../core/**
//go:test:include ../../../dagql/**
//go:test:include ../../../engine/**
//go:test:include ../../../internal/**
//go:test:include ../../../modules/**
//go:test:include ../../../network/**
//go:test:include ../../../sdk/**
//go:test:include ../../../toolchains/**
//go:test:include ../../../util/**
//go:test:include ../../../dagger.json
//go:test:include ../../../go.mod
//go:test:include ../../../go.sum
//go:test:include ../../../LICENSE
package e2e

import (
	"os"
	"testing"

	"dagger.io/dagger"
)

type harness struct {
	dag *dagger.Client
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	dag, err := dagger.Connect(t.Context(), dagger.WithLogOutput(os.Stderr))
	if err != nil {
		t.Fatalf("connect stable orchestration client: %v", err)
	}
	t.Cleanup(func() {
		if err := dag.Close(); err != nil {
			t.Errorf("close stable orchestration client: %v", err)
		}
	})

	// Privileged generic tests receive a core-only session. Explicitly serve
	// engine-dev and its dagger-cli dependency from the workspace so these tests
	// also work through a normally opened native session. The generic harness
	// creates a synthetic .git/HEAD; exclude it to avoid Go VCS stamping errors
	// and transferring repository history.
	projectSource := dag.CurrentWorkspace().Directory("/", dagger.WorkspaceDirectoryOpts{
		Exclude: []string{".git", ".git/**"},
	})
	if err := projectSource.AsModuleSource(dagger.DirectoryAsModuleSourceOpts{
		SourceRootPath: "toolchains/engine-dev",
	}).AsModule().Serve(t.Context(), dagger.ModuleServeOpts{IncludeDependencies: true}); err != nil {
		t.Fatalf("serve project build modules: %v", err)
	}

	return &harness{dag: dag}
}

func (h *harness) devCLIBinary() *dagger.File {
	// The released orchestration client has no generated types for this
	// repository's project-only modules, so attach their query selections to
	// otherwise empty core objects.
	q := h.dag.QueryBuilder().Select("daggerCli").Select("binary")
	return h.dag.File("e2e-placeholder", "").WithGraphQLQuery(q)
}

func (h *harness) devEngineService(name string) *dagger.Service {
	q := h.dag.QueryBuilder().Select("engineDev").Select("service").Arg("name", name)
	return h.dag.Container().AsService().WithGraphQLQuery(q)
}

func (h *harness) startDevEngine(t *testing.T, name string) (*dagger.Service, string) {
	t.Helper()

	engine, err := h.devEngineService(name).Start(t.Context())
	if err != nil {
		t.Fatalf("start development engine: %v", err)
	}
	endpoint, err := engine.Endpoint(t.Context(), dagger.ServiceEndpointOpts{Port: 1234, Scheme: "tcp"})
	if err != nil {
		t.Fatalf("resolve development engine endpoint: %v", err)
	}
	return engine, endpoint
}

func requireTargetExec(t *testing.T, target *dagger.Container, operation string) {
	t.Helper()
	ctx := t.Context()
	stdout, err := target.Stdout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := target.Stderr(ctx)
	if err != nil {
		t.Fatal(err)
	}
	exitCode, err := target.ExitCode(ctx)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("%s stdout:\n%s", operation, stdout)
	if stderr != "" {
		t.Logf("%s stderr:\n%s", operation, stderr)
	}
	if exitCode != 0 {
		t.Fatalf("%s: exit code %d", operation, exitCode)
	}
}
