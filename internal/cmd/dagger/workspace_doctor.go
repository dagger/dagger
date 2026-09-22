package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"dagger.io/dagger"
	workspacepkg "github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

var workspaceDoctorCmd = newWorkspaceDoctorCmd(false)
var doctorAliasCmd = newWorkspaceDoctorCmd(true)

func newWorkspaceDoctorCmd(hidden bool) *cobra.Command {
	return &cobra.Command{
		Use:    "doctor",
		Short:  "Check whether your workspace is ready to run Dagger",
		Hidden: hidden,
		Long: `Check workspace configuration, lockfile, engine connectivity, and Cloud authentication.

Missing configuration or lockfiles and Cloud authentication problems are warnings.
Invalid files or an unavailable engine cause a nonzero exit status.
Modules and their dependencies are not loaded. No workspace files are changed.`,
		Args: cobra.NoArgs,
		RunE: runWorkspaceDoctor,
	}
}

// doctorCloudAuthKey supplies the already checked credentials to engine setup.
type doctorCloudAuthKey struct{}

type doctorReport struct {
	out  io.Writer
	errs []error
}

func (r *doctorReport) result(name, detail string, err error, warning bool) {
	status := "PASS"
	if err != nil {
		detail = err.Error()
		status = "FAIL"
		if warning {
			status = "WARN"
		} else {
			r.errs = append(r.errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	if _, err := fmt.Fprintf(r.out, "%s %s: %s\n", status, name, detail); err != nil {
		r.errs = append(r.errs, err)
	}
}

func runWorkspaceDoctor(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	report := &doctorReport{out: cmd.OutOrStdout()}
	ref := workspaceRef
	if ref == "" {
		ref = sessionWorkspace
	}
	remote := isObviouslyRemoteWorkspaceRef(ref)
	if !remote {
		if ref == "" {
			ref = "."
		}
		cwd, err := filepath.Abs(ref)
		var ws *workspacepkg.Workspace
		if err == nil {
			var info os.FileInfo
			info, err = os.Stat(cwd)
			if err == nil && !info.IsDir() {
				err = fmt.Errorf("workspace %q is not a directory", ref)
			}
		}
		if err == nil {
			ws, err = workspacepkg.Detect(ctx, localPathExists, cwd)
		}
		if err != nil {
			report.result("Workspace", "", err, false)
		} else {
			doctorWorkspaceFiles(ctx, report, ws, os.ReadFile)
		}
	}

	cloudAuth, authErr := cloudCLI.cloudAuthWithLogin(ctx, false)
	report.result("Cloud login", "credentials available", authErr, true)

	// Reuse the diagnostic auth result so malformed Cloud credentials do not
	// prevent connecting to a local engine. A Cloud engine still requires auth.
	ctx = context.WithValue(ctx, doctorCloudAuthKey{}, cloudAuth)
	connected := false
	err := withEngine(ctx, client.Params{SkipWorkspaceModules: true}, func(ctx context.Context, c *client.Client) error {
		connected = true
		report.result("Engine", "connected", nil, false)
		ws := c.Dagger().CurrentWorkspace()
		if remote {
			doctorRemoteWorkspaceFiles(ctx, report, ws)
		}
		_, err := ws.ConfigRead(ctx)
		report.result("Workspace loading", "selected configuration and environment loaded", err, false)
		return nil
	})
	if err != nil {
		name := "Engine"
		if connected {
			name = "Engine session"
		}
		report.result(name, "", err, false)
	}
	return errors.Join(report.errs...)
}

// Read both files independently: a malformed config must not hide lock errors.
func doctorWorkspaceFiles(ctx context.Context, report *doctorReport, ws *workspacepkg.Workspace, readFile func(string) ([]byte, error)) {
	if ws == nil {
		report.result("Workspace config", "", errors.New("no workspace configuration found"), true)
		report.result("Lockfile", "", errors.New("no workspace lockfile found"), true)
		return
	}
	if ws.ConfigFile == "" {
		report.result("Workspace config", "", errors.New("no dagger.toml selected"), true)
	} else {
		data, err := readFile(filepath.Join(ws.Root, ws.ConfigFile))
		if err == nil {
			_, err = workspacepkg.ParseConfigAt(ctx, data, filepath.Dir(ws.ConfigFile))
		}
		report.result("Workspace config", ws.ConfigFile+" is loadable", err, false)
	}
	lockPath := filepath.Join(ws.Root, ws.LockFile)
	data, err := readFile(lockPath)
	if errors.Is(err, os.ErrNotExist) {
		lockPath = workspacepkg.LegacyLockFilePathForCanonical(lockPath)
		data, err = readFile(lockPath)
	}
	if errors.Is(err, os.ErrNotExist) {
		report.result("Lockfile", "", errors.New("no dagger.lock found"), true)
		return
	}
	if err == nil {
		_, err = workspacepkg.ParseLock(data)
	}
	report.result("Lockfile", lockPath+" is loadable", err, false)
}

func doctorRemoteWorkspaceFiles(ctx context.Context, report *doctorReport, ws *dagger.Workspace) {
	cwd, err := ws.Cwd(ctx)
	if err != nil {
		report.result("Workspace files", "", err, false)
		return
	}
	doctorWorkspaceFilesInRoot(ctx, report, cwd, func(dir string) ([]string, error) {
		return ws.Directory("/"+dir, dagger.WorkspaceDirectoryOpts{Include: []string{"*"}, Exclude: []string{"*/*"}}).Entries(ctx)
	}, func(name string) ([]byte, error) {
		contents, err := ws.File("/" + filepath.ToSlash(name)).Contents(ctx)
		return []byte(contents), err
	})
}

func doctorWorkspaceFilesInRoot(ctx context.Context, report *doctorReport, cwd string, listDir func(string) ([]string, error), readFile func(string) ([]byte, error)) {
	entries := map[string][]string{}
	var exists workspacepkg.PathExistsFunc
	exists = func(_ context.Context, name string) (string, bool, error) {
		dir := path.Dir(name)
		if dir != "." {
			_, found, err := exists(ctx, dir)
			if err != nil || !found {
				return dir, false, err
			}
		}
		names, ok := entries[dir]
		if !ok {
			var err error
			names, err = listDir(dir)
			if err != nil {
				return "", false, err
			}
			entries[dir] = names
		}
		return dir, slices.Contains(names, path.Base(name)) || slices.Contains(names, path.Base(name)+"/"), nil
	}
	detected, err := workspacepkg.DetectInRoot(ctx, exists, path.Clean(strings.TrimPrefix(cwd, "/")), ".")
	if err != nil {
		report.result("Workspace files", "", err, false)
		return
	}
	doctorWorkspaceFiles(ctx, report, detected, func(name string) ([]byte, error) {
		_, found, err := exists(ctx, name)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, os.ErrNotExist
		}
		return readFile(name)
	})
}
