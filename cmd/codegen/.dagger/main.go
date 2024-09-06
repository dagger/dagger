package main

import "dagger/codegen/internal/dagger"

type Codegen struct{}

// FIXME: stopgap until we can use *dagger.TypesSidecar
type Sidecar interface {
	dagger.DaggerObject
	Bind(client *dagger.Container) *dagger.Container
}

// Build the codegen binary
func (m *Codegen) Build(
	// +optional
	// +defaultPath="/"
	// +ignore=["!cmd/codegen", "!**/go.mod", "!**/go.sum", "!sdk/go"]
	source *dagger.Directory,
	// +optional
	platform dagger.Platform,
) *dagger.Directory {
	return dag.Go(source).
		Build(dagger.GoBuildOpts{
			Pkgs:      []string{"./cmd/codegen"},
			NoSymbols: true,
			NoDwarf:   true,
			Platform:  platform,
		})
}

func (m *Codegen) Dev(
	// +optional
	// +defaultPath="/"
	// +ignore=["!cmd/codegen", "!**/go.mod", "!**/go.sum", "!sdk/go"]
	source *dagger.Directory,
	// +optional
	platform dagger.Platform,
) *dagger.Container {
	return dag.Go(source).Env(dagger.GoEnvOpts{
		Platform: platform,
	})
}

func (m *Codegen) Binary(
	// +optional
	// +defaultPath="/"
	// +ignore=["!cmd/codegen", "!**/go.mod", "!**/go.sum", "!sdk/go"]
	source *dagger.Directory,
	// +optional
	platform dagger.Platform,
) *dagger.File {
	return dag.Go(source).
		Binary("./cmd/codegen", dagger.GoBinaryOpts{
			NoSymbols: true,
			NoDwarf:   true,
			Platform:  platform,
		})
}

func (m *Codegen) Container(
	// +optional
	// +defaultPath="/"
	// +ignore=["!cmd/codegen", "!**/go.mod", "!**/go.sum", "!sdk/go"]
	source *dagger.Directory,

	// +optional
	platform dagger.Platform,
) *dagger.Container {
	return dag.Wolfi().
		Container().
		WithFile("/usr/local/bin/codegen", m.Binary(source, platform)).
		WithEnvVariable("PATH", "$PATH:/usr/local/bin", dagger.ContainerWithEnvVariableOpts{
			Expand: true,
		}).
		WithDefaultTerminalCmd([]string{"sh"}, dagger.ContainerWithDefaultTerminalCmdOpts{
			ExperimentalPrivilegedNesting: true,
		})
}

func (m *Codegen) Codegen(
	// SDK language to generate for
	language string,

	// +optional
	// +defaultPath="/"
	// +ignore=["!cmd/codegen", "!**/go.mod", "!**/go.sum", "!sdk/go"]
	source *dagger.Directory,

	// +optional
	platform dagger.Platform,

	engine Sidecar,
) *dagger.Directory {
	return m.Container(source, platform).
		With(engine.Bind).
		WithExec(func() []string {
			cmd := []string{"codegen", "-o", "/output"}
			if language == "" {
				return cmd
			}
			return append(cmd, "--lang", language)
		}()).
		Directory("/output")
}

func (m *Codegen) Introspect(
	// +optional
	// +defaultPath="/"
	// +ignore=["!cmd/codegen", "!**/go.mod", "!**/go.sum", "!sdk/go"]
	source *dagger.Directory,

	// +optional
	platform dagger.Platform,

	engine Sidecar,
) *dagger.File {
	return m.Container(source, platform).
		With(engine.Bind).
		WithExec([]string{"codegen", "introspect", "-o", "/schema.json"}).
		File("/schema.json")
}
