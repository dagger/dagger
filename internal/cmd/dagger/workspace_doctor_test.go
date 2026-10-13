package daggercmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	workspacepkg "github.com/dagger/dagger/core/workspace"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceDoctorCommands(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		hidden bool
	}{
		{[]string{"workspace", "doctor"}, false},
		{[]string{"ws", "doctor"}, false},
		{[]string{"doctor"}, true},
	} {
		cmd, _, err := rootCmd.Find(tc.args)
		require.NoError(t, err)
		require.Equal(t, "doctor", cmd.Name())
		require.Equal(t, tc.hidden, cmd.Hidden)
		require.NoError(t, cmd.ValidateArgs(nil))
		require.Error(t, cmd.ValidateArgs([]string{"extra"}))
		require.NoError(t, validateFlagCapabilities(testRootCommand(), append(tc.args, "--engine=auto", "--workspace=.", "--env=dev")))
	}
}

func TestWorkspaceDoctorFiles(t *testing.T) {
	validLock, err := workspacepkg.NewLock().Marshal()
	require.NoError(t, err)
	for _, tc := range []struct {
		name     string
		files    map[string]string
		failures int
		output   []string
	}{
		{name: "valid", files: map[string]string{"dagger.toml": "", "dagger.lock": string(validLock)}, output: []string{"PASS Workspace config", "PASS Lockfile"}},
		{name: "missing", output: []string{"WARN Workspace config", "WARN Lockfile"}},
		{name: "both invalid", files: map[string]string{"dagger.toml": "[broken", "dagger.lock": "invalid"}, failures: 2, output: []string{"FAIL Workspace config", "FAIL Lockfile"}},
		{name: "legacy sdk field", files: map[string]string{"dagger.toml": "[modules.provider]\nsource = './sdk'\n[modules.provider.as-sdk]\nname = 'custom'\n", "dagger.lock": string(validLock)}, output: []string{"WARN Workspace config", "PASS Lockfile"}},
		{name: "unknown field", files: map[string]string{"dagger.toml": "[modules.provider]\nsource = './sdk'\npath = 'typo'\n", "dagger.lock": string(validLock)}, failures: 1, output: []string{"FAIL Workspace config", "PASS Lockfile"}},
		{name: "legacy lock", files: map[string]string{".dagger/lock": string(validLock)}, output: []string{"WARN Workspace config", "PASS Lockfile"}},
		{name: "invalid canonical wins", files: map[string]string{"dagger.lock": "broken", ".dagger/lock": string(validLock)}, failures: 1, output: []string{"FAIL Lockfile"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0700))
			for name, contents := range tc.files {
				filename := filepath.Join(root, name)
				require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0700))
				require.NoError(t, os.WriteFile(filename, []byte(contents), 0600))
			}
			nested := filepath.Join(root, "src")
			require.NoError(t, os.Mkdir(nested, 0700))
			ws, err := workspacepkg.Detect(t.Context(), localPathExists, nested)
			require.NoError(t, err)
			report := &doctorReport{}
			doctorWorkspaceFiles(t.Context(), report, ws, os.ReadFile)
			require.Len(t, report.errs, tc.failures)
			for _, want := range tc.output {
				require.Contains(t, doctorStatuses(report), want)
			}
			// Diagnostics never repair or regenerate files.
			for name, contents := range tc.files {
				data, err := os.ReadFile(filepath.Join(root, name))
				require.NoError(t, err)
				require.Equal(t, contents, string(data))
			}
		})
	}
}

func TestWorkspaceDoctorWarnings(t *testing.T) {
	report := &doctorReport{}
	doctorWorkspaceFiles(t.Context(), report, nil, os.ReadFile)
	report.result(t.Context(), "Cloud login", "", errors.New("not authenticated"), true)
	require.Empty(t, report.errs)
	require.Contains(t, doctorStatuses(report), "WARN Cloud login")
	report.result(t.Context(), "Engine", "", errors.New("connection refused"), false)
	require.Len(t, report.errs, 1)
	require.Contains(t, doctorStatuses(report), "FAIL Engine")
}

func TestWorkspaceDoctorRemoteFiles(t *testing.T) {
	for _, cwd := range []string{"/", "/src"} {
		t.Run(cwd, func(t *testing.T) {
			report := &doctorReport{}
			doctorWorkspaceFilesInRoot(t.Context(), report, cwd, func(dir string) ([]string, error) {
				switch dir {
				case ".":
					return []string{"src/", "dagger.toml"}, nil
				case "src":
					return nil, nil
				default:
					t.Fatalf("listed nonexistent directory %q", dir)
					return nil, nil
				}
			}, func(name string) ([]byte, error) {
				require.Equal(t, "dagger.toml", name)
				return nil, nil
			})
			require.Empty(t, report.errs)
			require.Contains(t, doctorStatuses(report), "PASS Workspace config")
			require.Contains(t, doctorStatuses(report), "WARN Lockfile")
		})
	}
}

func doctorStatuses(report *doctorReport) []string {
	statuses := make([]string, 0, len(report.results))
	for _, result := range report.results {
		statuses = append(statuses, string(result.Status)+" "+result.Name)
	}
	return statuses
}
