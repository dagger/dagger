package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/project"
	"github.com/stretchr/testify/require"
)

func TestProjectCLI(t *testing.T) {
	tmpdir := t.TempDir()

	// setup a local extension
	extensionDir := filepath.Join(tmpdir, "ext")
	require.NoError(t, os.Mkdir(extensionDir, 0755))

	mainContents, err := os.ReadFile("./testdata/extension/main.go")
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(extensionDir, "main.go"), mainContents, 0600)
	require.NoError(t, err)

	// init sdk-less project
	cmd := exec.Command("dagger", "project", "init", "--name", "test")
	cmd.Dir = tmpdir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	configPath := filepath.Join(cmd.Dir, "dagger.json")
	requireExpectedConfigAtPath(t, project.Config{
		Name:       "test",
		SDK:        "",
		Extensions: nil,
	}, configPath)

	// init sdk project
	cmd = exec.Command("dagger", "project", "init", "--name", "ext", "--sdk", "go")
	cmd.Dir = extensionDir
	out, err = cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	extConfigPath := filepath.Join(extensionDir, "dagger.json")
	requireExpectedConfigAtPath(t, project.Config{
		Name:       "ext",
		SDK:        "go",
		Extensions: nil,
	}, extConfigPath)

	// add local
	cmd = exec.Command("dagger", "project", "add", "local", "--path", "./ext/dagger.json")
	cmd.Dir = tmpdir
	out, err = cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	expectedConfig := project.Config{
		Name: "test",
		SDK:  "",
		Extensions: map[string]project.Extension{
			"ext": {
				Local: &project.LocalExtension{
					Path: "./ext/dagger.json",
				},
			},
		},
	}
	requireExpectedConfigAtPath(t, expectedConfig, configPath)

	// TODO: test adding git (hard because it requires a git repo that would need to change everytime there's a change to the format/this test)

	// show project
	cmd = exec.Command("dagger", "project")
	cmd.Dir = tmpdir
	out, err = cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	requireExpectedConfigBytes(t, expectedConfig, out)

	// rm extension
	cmd = exec.Command("dagger", "project", "rm", "--name", "ext")
	cmd.Dir = tmpdir
	out, err = cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	requireExpectedConfigAtPath(t, project.Config{
		Name:       "test",
		SDK:        "",
		Extensions: nil,
	}, configPath)
}

func requireExpectedConfigBytes(t *testing.T, expected project.Config, actualBytes []byte) {
	actual := project.Config{}
	require.NoError(t, json.Unmarshal(actualBytes, &actual))
	require.Equal(t, expected, actual)
}

func requireExpectedConfigAtPath(t *testing.T, expected project.Config, pathToActual string) {
	t.Helper()
	actualBytes, err := os.ReadFile(pathToActual)
	require.NoError(t, err)
	requireExpectedConfigBytes(t, expected, actualBytes)
}
