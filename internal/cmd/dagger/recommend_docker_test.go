package daggercmd

import (
	"testing"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/stretchr/testify/require"
)

func TestDockerRecommendations(t *testing.T) {
	ctx := t.Context()
	dag, err := dagger.Connect(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dag.Close()) })

	var docker registryModule
	for _, mod := range loadModuleRegistry() {
		if mod.Name == "docker" {
			docker = mod
			break
		}
	}
	require.Equal(t, "dagger.io/docker", docker.Repo)
	require.NotNil(t, docker.Recommend)

	files := []string{
		"Dockerfile", "dockerfile",
		"Dockerfile.dev", "dockerfile.prod",
		"test.Dockerfile", "lint.dockerfile",
		".dockerignore", "Dockerfile.dockerignore", "lint.dockerfile.dockerignore",
		"compose.yaml", "compose.yml",
		"compose.override.yaml", "compose.override.yml", "compose.prod.yaml", "compose.test.yml",
		"docker-compose.yaml", "docker-compose.yml",
		"docker-compose.override.yaml", "docker-compose.override.yml", "docker-compose.prod.yaml", "docker-compose.test.yml",
		".hadolint.yaml", ".hadolint.yml",
	}
	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			dir := core.NewQuery(dag).Directory().
				WithNewFile(file, "").
				WithNewFile("app/"+file, "")
			ws := dir.AsWorkspace(core.DirectoryAsWorkspaceOpts{Cwd: "/app"})
			matches, err := docker.Recommend(ctx, ws)
			require.NoError(t, err)
			require.Contains(t, matches, file)
			require.Contains(t, matches, "app/"+file)

			recs, err := recommendModules(ctx, ws, []registryModule{docker}, nil)
			require.NoError(t, err)
			require.Len(t, recs, 1)
			require.Equal(t, "dagger.io/docker", recs[0].Module.Repo)
		})
	}

	t.Run("unrelated and excluded files", func(t *testing.T) {
		dir := core.NewQuery(dag).Directory().WithNewDirectory("app")
		for _, file := range []string{"README.md", "docker.txt", "DockerfileGuide.md", "compose.json", "compose.yaml.bak", "docker-compose.yml.bak", ".gitignore"} {
			dir = dir.WithNewFile(file, "")
		}
		for _, excluded := range []string{".git", ".dagger", "node_modules", "vendor", "dist", "build", "target"} {
			for _, prefix := range []string{excluded + "/", "app/" + excluded + "/"} {
				for _, file := range files {
					dir = dir.WithNewFile(prefix+file, "")
				}
			}
		}
		ws := dir.AsWorkspace(core.DirectoryAsWorkspaceOpts{Cwd: "/app"})
		matches, err := docker.Recommend(ctx, ws)
		require.NoError(t, err)
		require.Empty(t, matches)
	})

	t.Run("already installed", func(t *testing.T) {
		ws := core.NewQuery(dag).Directory().WithNewFile("Dockerfile", "FROM scratch\n").AsWorkspace()
		recs, err := recommendModules(ctx, ws, []registryModule{docker}, map[string]bool{"docker": true})
		require.NoError(t, err)
		require.Empty(t, recs)
	})
}
