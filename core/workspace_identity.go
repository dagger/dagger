package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencontainers/go-digest"

	workspacepkg "github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/util/hashutil"
)

// ContentIdentity returns a digest of everything a caller can observe through
// ws, when ws can have one: its source is a Git ref or an in-engine directory,
// or edits on top of one. Two workspaces with the same identity are
// interchangeable, so a workspace given this digest as its content shares cache
// hits with every other workspace that has it, across calls, sessions and
// engines.
//
// ok is false for host-backed and rootless workspaces: their files are read
// from a client's host, where they can change between calls without the
// workspace value changing.
//
// The digest covers every field except the client route (ClientID and the
// host path), which says where host operations go, not what the workspace
// holds. Live remote reads, such as a Git remote's current tags, are not part
// of the workspace and are not covered.
func (ws *Workspace) ContentIdentity(ctx context.Context) (_ digest.Digest, ok bool, _ error) {
	if ws == nil || !contentIdentifiableSource(ws.source) {
		return "", false, nil
	}

	parts := []string{"workspace-content-identity/v1"}
	addResult := func(label string, res interface {
		ContentPreferredDigest(context.Context) (digest.Digest, error)
	}, present bool) error {
		if !present {
			parts = append(parts, label, "")
			return nil
		}
		dgst, err := res.ContentPreferredDigest(ctx)
		if err != nil {
			return fmt.Errorf("workspace content identity: %s: %w", label, err)
		}
		parts = append(parts, label, dgst.String())
		return nil
	}
	addJSON := func(label string, v any) error {
		bs, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("workspace content identity: %s: %w", label, err)
		}
		parts = append(parts, label, string(bs))
		return nil
	}

	var addSource func(label string, src WorkspaceSource) error
	addSource = func(label string, src WorkspaceSource) error {
		switch src := src.(type) {
		case *WorkspaceSourceGitRef:
			parts = append(parts, label, "gitRef")
			return addResult(label+".ref", src.Ref, src.Ref.Self() != nil)
		case *WorkspaceSourceDirectory:
			parts = append(parts, label, "directory")
			return addResult(label+".root", src.Root, src.Root.Self() != nil)
		case *WorkspaceSourceOverlay:
			parts = append(parts, label, "overlay")
			if err := addSource(label+".base", src.Base); err != nil {
				return err
			}
			if err := addJSON(label+".touchedPaths", src.TouchedPaths); err != nil {
				return err
			}
			if err := addJSON(label+".seededPaths", src.SeededPaths); err != nil {
				return err
			}
			return addResult(label+".changes", src.Changes, src.Changes.Self() != nil)
		default:
			return fmt.Errorf("workspace content identity: unsupported source %T", src)
		}
	}

	if err := addSource("source", ws.source); err != nil {
		return "", false, err
	}
	if err := addResult("rootfs", ws.rootfs, ws.rootfs.Self() != nil); err != nil {
		return "", false, err
	}
	if err := addResult("mounts", ws.mounts, ws.mounts.Self() != nil); err != nil {
		return "", false, err
	}
	for _, field := range []struct {
		label string
		value any
	}{
		{"mountPoints", ws.mountPoints},
		{"compatWorkspace", ws.compatWorkspace},
		{"ignorePatterns", ws.ignorePatterns},
		{"userConfigKey", ws.userConfigKey},
		{"selectedEnv", ws.selectedEnv},
		{"moduleClients", ws.moduleClients},
		{"clientModules", ws.clientModules},
		{"address", ws.Address},
		{"cwd", ws.Cwd},
		{"configFile", ws.ConfigFile},
		{"lockFile", ws.LockFile},
	} {
		if err := addJSON(field.label, field.value); err != nil {
			return "", false, err
		}
	}
	// The overlay's own encoding keeps setting values' types and sorts keys.
	userConfigOverlay, err := workspacepkg.EncodeUserWorkspaceOverlay(ws.userConfigOverlay)
	if err != nil {
		return "", false, fmt.Errorf("workspace content identity: userConfigOverlay: %w", err)
	}
	parts = append(parts, "userConfigOverlay", userConfigOverlay)
	return hashutil.HashStrings(parts...), true, nil
}

// HasContentIdentity reports whether ws has a ContentIdentity: its source is a
// Git ref or an in-engine directory, or edits on top of one.
func (ws *Workspace) HasContentIdentity() bool {
	return ws != nil && contentIdentifiableSource(ws.source)
}

func contentIdentifiableSource(src WorkspaceSource) bool {
	switch src := src.(type) {
	case *WorkspaceSourceGitRef, *WorkspaceSourceDirectory:
		return true
	case *WorkspaceSourceOverlay:
		return contentIdentifiableSource(src.Base)
	default:
		return false
	}
}
