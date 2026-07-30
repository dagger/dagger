package secretprovider

import (
	"os"
	"path/filepath"
)

// findWorkspaceRoot walks up from the current working directory looking for
// a dagger.json, so that relative cmd:// secret paths resolve consistently
// regardless of which subdirectory `dagger` was invoked from.
//
// This mirrors (independently, client-side) the workspace-root resolution
// the engine already does server-side for module source paths declared in
// dagger.json - see engine/server/session_workspaces.go. It's duplicated
// here rather than shared because that resolution walks the *client's*
// filesystem through an RPC-backed StatFS abstraction, which isn't
// available yet at the point the client sets up its local secret provider.
//
// Returns "" if no dagger.json is found (e.g. dagger invoked outside of
// any workspace), in which case cmd:// falls back to today's behavior.
func findWorkspaceRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "dagger.json")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
