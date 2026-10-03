package core

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
)

// rootPreservingDirectoryFields are the Directory fields whose result has its
// receiver's layout: what is at a path in one is at the same path in the other.
var rootPreservingDirectoryFields = map[string]bool{
	"filter":           true,
	"withFile":         true,
	"withFiles":        true,
	"withNewFile":      true,
	"withoutFile":      true,
	"withoutFiles":     true,
	"withDirectory":    true,
	"withNewDirectory": true,
	"withoutDirectory": true,
	"withSymlink":      true,
	"withTimestamps":   true,
	"withPatch":        true,
	"withPatchFile":    true,
	"withChanges":      true,
}

// workspaceTreePrefix reports where in the workspace root the tree a recipe
// builds sits, when it is a read of the workspace: e.g. "sub" for a read of
// the workspace cwd "sub", which is how vito/editor's tools read it, or "."
// for the root itself. A changeset measured from that tree applies there.
//
// It follows the recipe structurally, evaluating nothing, down to the
// workspace's state (leaves, by recipe digest: the workspace itself, or its
// root tree, which a read such as Workspace.directory may return as it is),
// through subdirectory reads and the Directory operations that keep their
// receiver's layout. A read of a host-backed workspace is a host directory
// read, placed by its path within the workspace's host path. Anything else,
// e.g. a tree read back out of a container, is not placed: ok is false.
func workspaceTreePrefix(id *call.ID, leaves map[digest.Digest]bool, ws *Workspace) (prefix string, ok bool) {
	if id == nil || id.IsHandle() {
		return "", false
	}
	if leaves[id.Digest()] {
		return ".", true
	}
	receiver := id.Receiver()
	if receiver == nil || receiver.Type() == nil || id.Module() != nil {
		return "", false
	}
	switch parent, field := receiver.Type().NamedType(), id.Field(); {
	case parent == "Workspace" && field == "directory":
		if !leaves[stableIDDigest(receiver)] {
			return "", false
		}
		p, ok := pathArg(id)
		if !ok {
			p = "."
		}
		if strings.HasPrefix(p, "/") {
			return withinTree(strings.TrimPrefix(p, "/"))
		}
		return withinTree(path.Join(ws.Cwd, p))
	case parent == "Host" && field == "directory":
		return hostReadPrefix(id, ws)
	case parent == "Directory":
		return directoryTreePrefix(id, receiver, leaves, ws)
	}
	return "", false
}

// directoryTreePrefix is workspaceTreePrefix for a Directory field.
func directoryTreePrefix(id, receiver *call.ID, leaves map[digest.Digest]bool, ws *Workspace) (string, bool) {
	switch field := id.Field(); {
	case field == "directory":
		base, ok := workspaceTreePrefix(receiver, leaves, ws)
		sub, hasPath := pathArg(id)
		if !ok || !hasPath {
			return "", false
		}
		return withinTree(path.Join(base, strings.TrimPrefix(sub, "/")))
	case field == "withDirectory" && receiver.Field() == "directory" && receiver.Receiver() == nil:
		// An empty directory with a tree copied to its root: the copy, e.g.
		// a filtered workspace read.
		if dest, _ := pathArg(id); path.Clean("/"+dest) != "/" {
			return "", false
		}
		source := id.Arg("source")
		if source == nil {
			return "", false
		}
		lit, ok := source.Value().(*call.LiteralID)
		if !ok {
			return "", false
		}
		return workspaceTreePrefix(lit.Value(), leaves, ws)
	case rootPreservingDirectoryFields[field]:
		return workspaceTreePrefix(receiver, leaves, ws)
	}
	return "", false
}

// hostReadPrefix places a host directory read within a host-backed
// workspace's host path.
func hostReadPrefix(id *call.ID, ws *Workspace) (string, bool) {
	hostRoot := ws.HostPath()
	p, ok := pathArg(id)
	if hostRoot == "" || !ok {
		return "", false
	}
	hostRoot = path.Clean(filepath.ToSlash(hostRoot))
	p = path.Clean(filepath.ToSlash(p))
	if p == hostRoot {
		return ".", true
	}
	rel, inside := strings.CutPrefix(p, hostRoot+"/")
	if !inside {
		return "", false
	}
	return withinTree(rel)
}

// pathArg returns a call's path argument.
func pathArg(id *call.ID) (string, bool) {
	arg := id.Arg("path")
	if arg == nil {
		return "", false
	}
	lit, ok := arg.Value().(*call.LiteralString)
	if !ok {
		return "", false
	}
	return lit.Value(), true
}

// withinTree cleans a relative path, refusing one that leaves its tree.
func withinTree(p string) (string, bool) {
	p = path.Clean(p)
	if p == ".." || strings.HasPrefix(p, "../") || path.IsAbs(p) {
		return "", false
	}
	return p, true
}
