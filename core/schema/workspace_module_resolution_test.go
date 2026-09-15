package schema

import (
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceModuleResolutionLine(t *testing.T) {
	src := &core.ModuleSource{SourceRootSubpath: ".", Git: &core.GitModuleSource{
		CloneRef:         "vanity.example/tools",
		ResolvedCloneRef: "https://github.com/acme/tools.git",
		Version:          "v1.4.2",
	}}
	require.Equal(t, "go.example/tools@v1 ➡️ https://github.com/acme/tools.git#v1.4.2",
		workspaceModuleResolutionLine("go.example/tools@v1", src))
	src.Git.ResolvedCloneRef = "ssh://git@github.com/acme/tools.git"
	src.SourceRootSubpath = "tools/go"
	require.Equal(t, "go.example/tools@v1 ➡️ ssh://git@github.com/acme/tools.git#v1.4.2:tools/go",
		workspaceModuleResolutionLine("go.example/tools@v1", src))
	src.Git.Version = ""
	require.Empty(t, workspaceModuleResolutionLine("go.example/tools@v1", src))
	require.Empty(t, workspaceModuleResolutionLine("./local", &core.ModuleSource{}))
}
