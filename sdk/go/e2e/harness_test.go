// Two layers live here. This package orchestrates: it builds a development CLI
// and engine out of the current source tree. The suites under testdata/ then run
// against what it built. Both import the Go client from this repo; this module
// exists to keep their test-only dependencies out of sdk/go.
//
// FIXME: remove these.
//
// The engine does not need them: engine-dev loads its own sources, and this
// suite reads everything else through CurrentWorkspace(). They are here because
// when the suite runs inside a container, that container's uploaded context is
// the workspace, so the tree has to be carried in. The sdk/go entry is separate
// again: `replace dagger.io/dagger => ..` makes the Go toolchain read
// sdk/go/go.mod off disk before any Dagger call happens, and that goes away with
// the replace once dagger/dagger#13701 lands.
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
//go:test:include ./dagger.toml
package e2e

import (
	"os"
	"testing"

	"dagger.io/dagger"
)

const engineDevCLIPath = "/usr/local/bin/dagger"

type harness struct {
	dag *dagger.Client
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	dag, err := dagger.Connect(
		t.Context(),
		dagger.WithLogOutput(os.Stderr),
	)
	if err != nil {
		t.Fatalf("connect orchestration client: %v", err)
	}
	t.Cleanup(func() {
		if err := dag.Close(); err != nil {
			t.Errorf("close orchestration client: %v", err)
		}
	})
	// Modules listed in dagger.toml normally appear in the session's API on their
	// own. Not here: the engine decides whether to include them once, when it
	// first sets up a client's workspace, and caches that answer
	// (ensureWorkspaceLoaded, engine/server/session_workspaces.go). `dagger api
	// exec` sets us up with the built-in API only, and opting in afterwards does
	// not re-run the decision. So load it by hand — ModuleSource reads the
	// workspace files directly, and Serve adds the module to this session's API.
	if err := dag.CurrentWorkspace().ModuleSource("/toolchains/engine-dev").AsModule().Serve(t.Context()); err != nil {
		t.Fatalf("serve engine-dev module: %v", err)
	}
	return &harness{dag: dag}
}

func (h *harness) devCLIBinary(t *testing.T) *dagger.File {
	t.Helper()

	var result struct {
		EngineDev struct {
			Container struct {
				File struct {
					ID dagger.ID `json:"id"`
				} `json:"file"`
			} `json:"container"`
		} `json:"engineDev"`
	}
	if err := h.dag.Do(t.Context(), &dagger.Request{
		Query: `query EngineDevCLI($path: String!) {
  engineDev {
    container {
      file(path: $path) {
        id
      }
    }
  }
}`,
		Variables: map[string]any{
			"path": engineDevCLIPath,
		},
		OpName: "EngineDevCLI",
	}, &dagger.Response{Data: &result}); err != nil {
		t.Fatalf("build development CLI: %v", err)
	}

	return dagger.Ref[*dagger.File](h.dag, result.EngineDev.Container.File.ID)
}

func (h *harness) devEngineService(t *testing.T, name string) *dagger.Service {
	t.Helper()

	var result struct {
		EngineDev struct {
			Service struct {
				ID dagger.ID `json:"id"`
			} `json:"service"`
		} `json:"engineDev"`
	}
	if err := h.dag.Do(t.Context(), &dagger.Request{
		Query: `query EngineDevService($name: String!) {
  engineDev {
    service(name: $name) {
      id
    }
  }
}`,
		Variables: map[string]any{
			"name": name,
		},
		OpName: "EngineDevService",
	}, &dagger.Response{Data: &result}); err != nil {
		t.Fatalf("create development engine service: %v", err)
	}

	return dagger.Ref[*dagger.Service](h.dag, result.EngineDev.Service.ID)
}

func (h *harness) startDevEngine(t *testing.T, name string) (*dagger.Service, string) {
	t.Helper()

	engine, err := h.devEngineService(t, name).Start(t.Context())
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
