package daggercmd

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client/pathutil"
	enginetel "github.com/dagger/dagger/engine/telemetry"
)

// cloudWorkspace returns the git remote and the workspace path that the CLI
// sends to Dagger Cloud when it requests an engine. Dagger Cloud uses them to
// tell apart the runs of one command in different repositories, and in
// different workspaces of one repository.
//
// The remote is the dagger.io/git.remote label (host and path, without
// credentials). The path is the directory of the selected dagger.toml,
// relative to the git root, and empty without one. Both are empty when the
// workspace is not in the git checkout of the working directory, for example
// with a remote -W. The remote is empty when origin is a local path.
func cloudWorkspace(ctx context.Context, ref *string) (remote, path string) {
	cwd, err := pathutil.Getwd()
	if err != nil {
		return "", ""
	}
	local, err := workspace.Detect(ctx, localPathExists, cwd)
	if err != nil || local == nil {
		return "", ""
	}
	ws := local
	if ref != nil {
		if isObviouslyRemoteWorkspaceRef(*ref) {
			return "", ""
		}
		ws, err = workspace.Detect(ctx, localPathExists, *ref)
		if err != nil || ws == nil || ws.Root != local.Root {
			return "", ""
		}
	}
	remote, _ = enginetel.LoadDefaultLabels(cwd, engine.Version).Get("dagger.io/git.remote")
	if host, _, ok := strings.Cut(remote, "/"); !ok || !strings.Contains(host, ".") {
		remote = ""
	}
	if ws.ConfigFile != "" {
		path = filepath.ToSlash(filepath.Dir(ws.ConfigFile))
	}
	return remote, path
}
