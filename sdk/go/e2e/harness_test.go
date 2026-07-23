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

const (
	engineDevModuleRef = "../../../toolchains/engine-dev"
	engineDevCLIPath   = "/usr/local/bin/dagger"
)

// Keep in sync with the go:test:include directives above. ModuleSource starts
// with the engine-dev module's narrow context; these patterns expand it to the
// repository inputs needed to build the development CLI and engine.
var engineDevSourceIncludes = []string{
	"../../analytics/**",
	"../../auth/**",
	"../../cmd/**",
	"../../core/**",
	"../../dagql/**",
	"../../engine/**",
	"../../internal/**",
	"../../modules/**",
	"../../network/**",
	"../../sdk/**",
	"../../toolchains/**",
	"../../util/**",
	"../../dagger.json",
	"../../go.mod",
	"../../go.sum",
	"../../LICENSE",
}

type harness struct {
	dag      *dagger.Client
	sourceID dagger.ID
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
	moduleSource := dag.ModuleSource(engineDevModuleRef).WithIncludes(engineDevSourceIncludes)
	if err := moduleSource.AsModule().Serve(t.Context()); err != nil {
		t.Fatalf("serve engine-dev module: %v", err)
	}
	sourceID, err := moduleSource.ContextDirectory().ID(t.Context())
	if err != nil {
		t.Fatalf("load engine-dev source directory: %v", err)
	}

	return &harness{dag: dag, sourceID: sourceID}
}

func (h *harness) devCLIBinary(t *testing.T) *dagger.File {
	t.Helper()

	var result struct {
		EngineDev struct {
			Container struct {
				File struct {
					ID dagger.FileID `json:"id"`
				} `json:"file"`
			} `json:"container"`
		} `json:"engineDev"`
	}
	if err := h.dag.Do(t.Context(), &dagger.Request{
		Query: `query EngineDevCLI($source: ID!, $path: String!) {
  engineDev(source: $source) {
    container {
      file(path: $path) {
        id
      }
    }
  }
}`,
		Variables: map[string]any{
			"source": h.sourceID,
			"path":   engineDevCLIPath,
		},
		OpName: "EngineDevCLI",
	}, &dagger.Response{Data: &result}); err != nil {
		t.Fatalf("build development CLI: %v", err)
	}

	return h.dag.LoadFileFromID(result.EngineDev.Container.File.ID)
}

func (h *harness) devEngineService(t *testing.T, name string) *dagger.Service {
	t.Helper()

	var result struct {
		EngineDev struct {
			Service struct {
				ID dagger.ServiceID `json:"id"`
			} `json:"service"`
		} `json:"engineDev"`
	}
	if err := h.dag.Do(t.Context(), &dagger.Request{
		Query: `query EngineDevService($source: ID!, $name: String!) {
  engineDev(source: $source) {
    service(name: $name) {
      id
    }
  }
}`,
		Variables: map[string]any{
			"source": h.sourceID,
			"name":   name,
		},
		OpName: "EngineDevService",
	}, &dagger.Response{Data: &result}); err != nil {
		t.Fatalf("create development engine service: %v", err)
	}

	return h.dag.LoadServiceFromID(result.EngineDev.Service.ID)
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
