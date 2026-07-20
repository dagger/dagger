package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	workspacepkg "github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

// workspaceInvalidator drops the cached per-client workspace detection so the
// next access re-detects from the host. Set by engine/server (which owns the
// per-client cache); nil in contexts without a server, where invalidation is a
// no-op.
var workspaceInvalidator func(context.Context) error

// SetWorkspaceInvalidator registers the hook used to drop the current client's
// cached workspace detection. Mirrors SetModuleSourceSDKLoader.
func SetWorkspaceInvalidator(fn func(context.Context) error) {
	workspaceInvalidator = fn
}

// InvalidateCurrentWorkspace drops the calling client's cached workspace
// detection so the next access re-detects it from the host. Used after writing
// workspace config files to the host (e.g. applying a migration changeset),
// since the per-client cache would otherwise keep serving the pre-write view
// for the lifetime of the client — which, under nested execution, spans more
// than one session.
func InvalidateCurrentWorkspace(ctx context.Context) error {
	if workspaceInvalidator == nil {
		return nil
	}
	return workspaceInvalidator(ctx)
}

// Workspace represents a detected workspace in the dagql schema.
type Workspace struct {
	// source is the private backing source for workspace filesystem and git
	// behavior. It is intentionally not exposed through GraphQL.
	source WorkspaceSource

	// rootfs is the pre-fetched root filesystem for remote workspaces.
	// Internal only — not exposed in GraphQL. Local workspaces resolve
	// directories lazily via per-call host.directory() instead.
	rootfs dagql.ObjectResult[*Directory]

	// compatWorkspace stores the originating compat-workspace projection when
	// this workspace was loaded from a legacy dagger.json instead of an explicit
	// dagger.toml. Internal only.
	compatWorkspace *workspacepkg.CompatWorkspace

	Address    string `field:"true" doc:"Canonical Dagger address of the workspace location, or an opaque identity for synthetic workspaces."`
	Cwd        string
	ConfigFile string

	// LockFile is the selected lockfile path relative to the workspace root.
	// It is independent from ConfigFile: compat config and missing native config
	// can still have a writable local lockfile.
	LockFile string

	// ClientID is the ID of the client that created this workspace.
	// Used to route host filesystem operations through the correct session
	// when the workspace is passed to a module function.
	ClientID string

	// hostPath is the host filesystem path for the workspace boundary.
	// Internal only (not in GraphQL schema). Empty for remote workspaces.
	// Used by workspace filesystem operations that need host access.
	hostPath string
}

// WorkspaceSource is the private backing source for a Workspace.
//
// It is exported only so schema/server packages can pass source values around;
// the unexported method keeps implementations local to core.
type WorkspaceSource interface {
	workspaceSource()
}

type WorkspaceSourceClientLocal struct {
	HostPath string
}

func (*WorkspaceSourceClientLocal) workspaceSource() {}

type WorkspaceSourceRootlessLocal struct {
	HostPath string
}

func (*WorkspaceSourceRootlessLocal) workspaceSource() {}

type WorkspaceSourceDirectory struct {
	Root dagql.ObjectResult[*Directory]
}

func (*WorkspaceSourceDirectory) workspaceSource() {}

type WorkspaceSourceGitRef struct {
	Ref dagql.Result[*GitRef]
	// ExplicitCommit distinguishes a workspace requested by immutable commit SHA
	// from a mutable ref that happens to resolve to the same commit.
	ExplicitCommit bool
}

func (*WorkspaceSourceGitRef) workspaceSource() {}

type WorkspaceSourceOverlay struct {
	Base WorkspaceSource
	// TouchedPaths is the cumulative set of workspace-relative paths the
	// overlay's edits affect. Set only for host-backed (client-local) overlays,
	// where it sizes the sparse diff base: Changes.After is the accumulated
	// edits applied to an empty base (the delta root — it never references the
	// host tree) and Changes.Before is host.directory including only these
	// paths, so forcing the changeset syncs just the touched files instead of
	// uploading the whole workspace. Value/git/rootless overlays leave this nil
	// and diff full in-engine trees (nothing to upload).
	TouchedPaths []string
	Changes      dagql.ObjectResult[*Changeset]
}

func (*WorkspaceSourceOverlay) workspaceSource() {}

func NewWorkspaceSourceClientLocal(hostPath string) WorkspaceSource {
	return &WorkspaceSourceClientLocal{
		HostPath: hostPath,
	}
}

func NewWorkspaceSourceRootlessLocal(hostPath string) WorkspaceSource {
	return &WorkspaceSourceRootlessLocal{
		HostPath: hostPath,
	}
}

func NewWorkspaceSourceDirectory(root dagql.ObjectResult[*Directory]) WorkspaceSource {
	return &WorkspaceSourceDirectory{
		Root: root,
	}
}

func NewWorkspaceSourceGitRef(ref dagql.Result[*GitRef], explicitCommit bool) WorkspaceSource {
	return &WorkspaceSourceGitRef{
		Ref:            ref,
		ExplicitCommit: explicitCommit,
	}
}

func NewWorkspaceSourceOverlay(
	base WorkspaceSource,
	touchedPaths []string,
	changes dagql.ObjectResult[*Changeset],
) WorkspaceSource {
	if overlay, ok := base.(*WorkspaceSourceOverlay); ok {
		base = overlay.Base
	}
	// The caller accumulates TouchedPaths (union with the parent overlay's)
	// before constructing, so they are already cumulative here.
	return &WorkspaceSourceOverlay{
		Base:         base,
		TouchedPaths: touchedPaths,
		Changes:      changes,
	}
}

func (ws *Workspace) Source() WorkspaceSource {
	if ws == nil {
		return nil
	}
	if ws.source != nil {
		return ws.source
	}
	if ws.hostPath != "" {
		return NewWorkspaceSourceClientLocal(ws.hostPath)
	}
	if ws.rootfs.Self() != nil {
		return NewWorkspaceSourceDirectory(ws.rootfs)
	}
	return nil
}

func (ws *Workspace) SetSource(src WorkspaceSource) {
	ws.source = src
}

func (ws *Workspace) SourceDirectory() (dagql.ObjectResult[*Directory], bool) {
	if ws == nil {
		return dagql.ObjectResult[*Directory]{}, false
	}
	switch src := ws.Source().(type) {
	case *WorkspaceSourceDirectory:
		if src.Root.Self() != nil {
			return src.Root, true
		}
	case *WorkspaceSourceGitRef:
		if ws.rootfs.Self() != nil {
			return ws.rootfs, true
		}
	case *WorkspaceSourceOverlay:
		if _, local := src.Base.(*WorkspaceSourceClientLocal); local {
			// Host-backed overlays store no full tree: Changes.After is the
			// edits applied to an empty base (sparse), not host + edits.
			// Reads resolve per-call against the host instead (see
			// schema.resolveHostOverlayRootfs).
			return dagql.ObjectResult[*Directory]{}, false
		}
		if changes := src.Changes.Self(); changes != nil && changes.After.Self() != nil {
			return changes.After, true
		}
	}
	if ws.rootfs.Self() != nil {
		return ws.rootfs, true
	}
	return dagql.ObjectResult[*Directory]{}, false
}

func (ws *Workspace) SourceGitRef() (dagql.Result[*GitRef], bool) {
	ref, ok := workspaceSourceGitRef(ws.Source())
	return ref, ok
}

func workspaceSourceGitRef(src WorkspaceSource) (dagql.Result[*GitRef], bool) {
	switch src := src.(type) {
	case *WorkspaceSourceGitRef:
		if src.Ref.Self() != nil {
			return src.Ref, true
		}
	case *WorkspaceSourceOverlay:
		return workspaceSourceGitRef(src.Base)
	}
	return dagql.Result[*GitRef]{}, false
}

func (ws *Workspace) OverlayChanges() (dagql.ObjectResult[*Changeset], bool) {
	overlay, ok := ws.Source().(*WorkspaceSourceOverlay)
	if !ok || overlay.Changes.Self() == nil {
		return dagql.ObjectResult[*Changeset]{}, false
	}
	return overlay.Changes, true
}

// ClientLocalBase reports whether the workspace's base source is the client's
// local git-rooted host directory. False for rootless local workspaces (which
// also carry a host path but must not read through it) and for value/git
// workspaces.
func (ws *Workspace) ClientLocalBase() bool {
	if ws == nil {
		return false
	}
	_, ok := ws.BaseSource().(*WorkspaceSourceClientLocal)
	return ok
}

// OverlayDeltaRoot returns a host-backed overlay's accumulated edits applied to
// an empty base — the changeset's After side, which never references the host
// tree — or false if this workspace has no such overlay (a pristine workspace,
// or a value/git/rootless overlay whose After is a full tree).
func (ws *Workspace) OverlayDeltaRoot() (dagql.ObjectResult[*Directory], bool) {
	if !ws.ClientLocalBase() {
		return dagql.ObjectResult[*Directory]{}, false
	}
	overlay, ok := ws.Source().(*WorkspaceSourceOverlay)
	if !ok {
		return dagql.ObjectResult[*Directory]{}, false
	}
	changes := overlay.Changes.Self()
	if changes == nil || changes.After.Self() == nil {
		return dagql.ObjectResult[*Directory]{}, false
	}
	return changes.After, true
}

// OverlayTouchedPaths returns the cumulative set of workspace-relative paths the
// overlay's edits affect, used to size the sparse diff base.
func (ws *Workspace) OverlayTouchedPaths() []string {
	overlay, ok := ws.Source().(*WorkspaceSourceOverlay)
	if !ok {
		return nil
	}
	return overlay.TouchedPaths
}

// OverlayPathTouched reports whether the overlay's edits affect the given
// workspace-relative path, either directly or via a touched parent directory.
func (ws *Workspace) OverlayPathTouched(p string) bool {
	p = path.Clean(filepath.ToSlash(p))
	for _, touched := range ws.OverlayTouchedPaths() {
		touched = path.Clean(filepath.ToSlash(touched))
		if p == touched || strings.HasPrefix(p, touched+"/") {
			return true
		}
	}
	return false
}

func (ws *Workspace) BaseSource() WorkspaceSource {
	src := ws.Source()
	if overlay, ok := src.(*WorkspaceSourceOverlay); ok {
		return overlay.Base
	}
	return src
}

func (ws *Workspace) LocalSourceHostPath() (string, bool) {
	if ws == nil {
		return "", false
	}
	switch src := ws.BaseSource().(type) {
	case *WorkspaceSourceClientLocal:
		return src.HostPath, src.HostPath != ""
	case *WorkspaceSourceRootlessLocal:
		return src.HostPath, src.HostPath != ""
	default:
		return "", false
	}
}

func (ws *Workspace) ExportHostPath() (string, error) {
	if ws == nil {
		return "", fmt.Errorf("workspace is required")
	}
	switch src := ws.BaseSource().(type) {
	case *WorkspaceSourceClientLocal:
		if src.HostPath == "" {
			return "", fmt.Errorf("workspace export requires a local Git workspace")
		}
		return src.HostPath, nil
	case *WorkspaceSourceRootlessLocal:
		return "", fmt.Errorf("workspace export requires a local Git workspace")
	case *WorkspaceSourceGitRef:
		return "", fmt.Errorf("cannot export a remote Git workspace")
	case *WorkspaceSourceDirectory:
		return "", fmt.Errorf("cannot export a synthetic workspace")
	case nil:
		return "", fmt.Errorf("workspace export requires a local Git workspace")
	default:
		return "", fmt.Errorf("cannot export workspace source %T", src)
	}
}

func (ws *Workspace) IsValueWorkspace() bool {
	if ws == nil || ws.ClientID != "" {
		return false
	}
	switch ws.Source().(type) {
	case *WorkspaceSourceDirectory, *WorkspaceSourceGitRef, *WorkspaceSourceOverlay:
		return true
	default:
		return false
	}
}

// Rootfs returns the pre-fetched root filesystem directory for remote workspaces.
// Returns a zero value for local workspaces (they resolve lazily).
func (ws *Workspace) Rootfs() dagql.ObjectResult[*Directory] {
	if root, ok := ws.SourceDirectory(); ok {
		return root
	}
	return ws.rootfs
}

// SetRootfs sets the pre-fetched root filesystem (used by remote workspace setup).
func (ws *Workspace) SetRootfs(r dagql.ObjectResult[*Directory]) {
	ws.rootfs = r
}

// HostPath returns the internal host filesystem path for the workspace boundary.
// Returns empty string for remote workspaces (read-only).
func (ws *Workspace) HostPath() string {
	return ws.hostPath
}

// SetHostPath sets the internal host filesystem path.
func (ws *Workspace) SetHostPath(p string) {
	ws.hostPath = p
}

// CompatWorkspace returns the internal compat-workspace provenance for this
// workspace. Nil means this workspace was not loaded from legacy compat mode.
func (ws *Workspace) CompatWorkspace() *workspacepkg.CompatWorkspace {
	return ws.compatWorkspace
}

// SetCompatWorkspace sets the internal compat-workspace provenance.
func (ws *Workspace) SetCompatWorkspace(compat *workspacepkg.CompatWorkspace) {
	ws.compatWorkspace = compat
}

func (*Workspace) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Workspace",
		NonNull:   true,
	}
}

func (*Workspace) TypeDescription() string {
	return "A Dagger workspace detected from the current working directory or constructed from a Directory."
}

var _ dagql.PersistedObject = (*Workspace)(nil)
var _ dagql.PersistedObjectDecoder = (*Workspace)(nil)
var _ dagql.HasDependencyResults = (*Workspace)(nil)

type persistedWorkspacePayload struct {
	RootfsResultID  uint64                        `json:"rootfsResultID,omitempty"`
	Source          *persistedWorkspaceSource     `json:"source,omitempty"`
	CompatWorkspace *workspacepkg.CompatWorkspace `json:"compatWorkspace,omitempty"`
	Address         string                        `json:"address,omitempty"`
	Cwd             string                        `json:"cwd,omitempty"`
	ConfigFile      string                        `json:"configFile,omitempty"`
	LockFile        string                        `json:"lockFile,omitempty"`
	ClientID        string                        `json:"clientID,omitempty"`
	HostPath        string                        `json:"hostPath,omitempty"`

	// Decode-only names from main's pre-workspace-selection payload.
	LegacyPath       string `json:"path,omitempty"`
	LegacyConfigPath string `json:"configPath,omitempty"`
}

type persistedWorkspaceSource struct {
	Kind           string                    `json:"kind"`
	RootResultID   uint64                    `json:"rootResultID,omitempty"`
	GitRefResultID uint64                    `json:"gitRefResultID,omitempty"`
	ExplicitCommit bool                      `json:"explicitCommit,omitempty"`
	ChangesID      uint64                    `json:"changesID,omitempty"`
	TouchedPaths   []string                  `json:"touchedPaths,omitempty"`
	HostPath       string                    `json:"hostPath,omitempty"`
	Base           *persistedWorkspaceSource `json:"base,omitempty"`
}

const (
	persistedWorkspaceSourceClientLocal = "clientLocal"
	persistedWorkspaceSourceRootless    = "rootlessLocal"
	persistedWorkspaceSourceDirectory   = "directory"
	persistedWorkspaceSourceGitRef      = "gitRef"
	persistedWorkspaceSourceOverlay     = "overlay"
)

func encodePersistedWorkspaceSource(cache dagql.PersistedObjectCache, src WorkspaceSource) (*persistedWorkspaceSource, error) {
	switch src := src.(type) {
	case *WorkspaceSourceClientLocal:
		return &persistedWorkspaceSource{Kind: persistedWorkspaceSourceClientLocal}, nil
	case *WorkspaceSourceRootlessLocal:
		return &persistedWorkspaceSource{
			Kind:     persistedWorkspaceSourceRootless,
			HostPath: src.HostPath,
		}, nil
	case *WorkspaceSourceDirectory:
		payload := &persistedWorkspaceSource{Kind: persistedWorkspaceSourceDirectory}
		if src.Root.Self() != nil {
			rootID, err := encodePersistedObjectRef(cache, src.Root, "workspace directory source")
			if err != nil {
				return nil, err
			}
			payload.RootResultID = rootID
		}
		return payload, nil
	case *WorkspaceSourceGitRef:
		refID, err := encodePersistedObjectRef(cache, src.Ref, "workspace git ref source")
		if err != nil {
			return nil, err
		}
		return &persistedWorkspaceSource{
			Kind:           persistedWorkspaceSourceGitRef,
			GitRefResultID: refID,
			ExplicitCommit: src.ExplicitCommit,
		}, nil
	case *WorkspaceSourceOverlay:
		payload := &persistedWorkspaceSource{Kind: persistedWorkspaceSourceOverlay}
		if src.Base != nil {
			base, err := encodePersistedWorkspaceSource(cache, src.Base)
			if err != nil {
				return nil, err
			}
			payload.Base = base
		}
		payload.TouchedPaths = src.TouchedPaths
		if src.Changes.Self() != nil {
			changesID, err := encodePersistedObjectRef(cache, src.Changes, "workspace overlay changes")
			if err != nil {
				return nil, err
			}
			payload.ChangesID = changesID
		}
		return payload, nil
	default:
		return nil, fmt.Errorf("encode persisted workspace source: unsupported source %T", src)
	}
}

func decodePersistedWorkspaceSource(
	ctx context.Context,
	dag *dagql.Server,
	persisted *persistedWorkspaceSource,
	rootfs dagql.ObjectResult[*Directory],
	hostPath string,
) (WorkspaceSource, error) {
	if persisted == nil {
		return nil, nil
	}
	switch persisted.Kind {
	case persistedWorkspaceSourceClientLocal:
		return NewWorkspaceSourceClientLocal(hostPath), nil
	case persistedWorkspaceSourceRootless:
		rootlessHostPath := persisted.HostPath
		if rootlessHostPath == "" {
			rootlessHostPath = hostPath
		}
		return NewWorkspaceSourceRootlessLocal(rootlessHostPath), nil
	case persistedWorkspaceSourceDirectory:
		root := rootfs
		if persisted.RootResultID != 0 {
			var err error
			root, err = loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.RootResultID, "workspace directory source")
			if err != nil {
				return nil, err
			}
		}
		return NewWorkspaceSourceDirectory(root), nil
	case persistedWorkspaceSourceGitRef:
		if persisted.GitRefResultID == 0 {
			return nil, fmt.Errorf("decode persisted workspace source: gitRef missing result ID")
		}
		ref, err := loadPersistedObjectResultByResultID[*GitRef](ctx, dag, persisted.GitRefResultID, "workspace git ref source")
		if err != nil {
			return nil, err
		}
		return NewWorkspaceSourceGitRef(ref.Result, persisted.ExplicitCommit), nil
	case persistedWorkspaceSourceOverlay:
		base, err := decodePersistedWorkspaceSource(ctx, dag, persisted.Base, rootfs, hostPath)
		if err != nil {
			return nil, err
		}
		var changes dagql.ObjectResult[*Changeset]
		if persisted.ChangesID != 0 {
			changes, err = loadPersistedObjectResultByResultID[*Changeset](ctx, dag, persisted.ChangesID, "workspace overlay changes")
			if err != nil {
				return nil, err
			}
		}
		return NewWorkspaceSourceOverlay(base, persisted.TouchedPaths, changes), nil
	default:
		return nil, fmt.Errorf("decode persisted workspace source: unsupported source kind %q", persisted.Kind)
	}
}

func (ws *Workspace) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	if ws == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted workspace: nil workspace")
	}

	payload := persistedWorkspacePayload{
		CompatWorkspace: ws.compatWorkspace,
		Address:         ws.Address,
		Cwd:             ws.Cwd,
		ConfigFile:      ws.ConfigFile,
		LockFile:        ws.LockFile,
		ClientID:        ws.ClientID,
		HostPath:        ws.hostPath,
	}
	if ws.rootfs.Self() != nil {
		rootfsID, err := encodePersistedObjectRef(cache, ws.rootfs, "workspace rootfs")
		if err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
		payload.RootfsResultID = rootfsID
	}
	if ws.Source() != nil {
		source, err := encodePersistedWorkspaceSource(cache, ws.Source())
		if err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
		payload.Source = source
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("marshal persisted workspace payload: %w", err)
	}
	return encodePersistedObjectRawJSON(payloadBytes), nil
}

func (*Workspace) DecodePersistedObject(
	ctx context.Context,
	dag *dagql.Server,
	_ uint64,
	_ *dagql.ResultCall,
	payload json.RawMessage,
) (dagql.Typed, error) {
	var persisted persistedWorkspacePayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted workspace payload: %w", err)
	}

	var rootfs dagql.ObjectResult[*Directory]
	if persisted.RootfsResultID != 0 {
		var err error
		rootfs, err = loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.RootfsResultID, "workspace rootfs")
		if err != nil {
			return nil, err
		}
	}

	cwd := persisted.Cwd
	if cwd == "" {
		cwd = persisted.LegacyPath
	}
	configFile := persisted.ConfigFile
	if configFile == "" {
		configFile = persisted.LegacyConfigPath
	}
	lockFile := persisted.LockFile
	if lockFile == "" && configFile != "" {
		lockFile = filepath.Join(filepath.Dir(configFile), workspacepkg.LockFileName)
	}
	lockFile = workspacepkg.CanonicalLockFilePath(lockFile)

	ws := &Workspace{
		rootfs:          rootfs,
		compatWorkspace: persisted.CompatWorkspace,
		Address:         persisted.Address,
		Cwd:             cwd,
		ConfigFile:      configFile,
		LockFile:        lockFile,
		ClientID:        persisted.ClientID,
		hostPath:        persisted.HostPath,
	}
	if persisted.Source != nil {
		src, err := decodePersistedWorkspaceSource(ctx, dag, persisted.Source, rootfs, persisted.HostPath)
		if err != nil {
			return nil, err
		}
		ws.source = src
	}
	return ws, nil
}

func (ws *Workspace) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	_ = ctx
	if ws == nil || ws.rootfs.Self() == nil {
		if ws != nil && ws.source != nil {
			return attachWorkspaceSource(attach, ws.source)
		}
		return nil, nil
	}

	attached, err := attach(ws.rootfs)
	if err != nil {
		return nil, fmt.Errorf("attach workspace rootfs: %w", err)
	}
	typed, ok := attached.(dagql.ObjectResult[*Directory])
	if !ok {
		return nil, fmt.Errorf("attach workspace rootfs: unexpected result %T", attached)
	}
	ws.rootfs = typed
	deps := []dagql.AnyResult{typed}
	if ws.source != nil {
		sourceDeps, err := attachWorkspaceSource(attach, ws.source)
		if err != nil {
			return nil, err
		}
		deps = append(deps, sourceDeps...)
	}
	return deps, nil
}

func attachWorkspaceSource(
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
	src WorkspaceSource,
) ([]dagql.AnyResult, error) {
	switch src := src.(type) {
	case nil, *WorkspaceSourceClientLocal, *WorkspaceSourceRootlessLocal:
		return nil, nil
	case *WorkspaceSourceDirectory:
		if src.Root.Self() == nil {
			return nil, nil
		}
		attached, err := attach(src.Root)
		if err != nil {
			return nil, fmt.Errorf("attach workspace directory source: %w", err)
		}
		root, ok := attached.(dagql.ObjectResult[*Directory])
		if !ok {
			return nil, fmt.Errorf("attach workspace directory source: unexpected result %T", attached)
		}
		src.Root = root
		return []dagql.AnyResult{root}, nil
	case *WorkspaceSourceGitRef:
		if src.Ref.Self() == nil {
			return nil, nil
		}
		attached, err := attach(src.Ref)
		if err != nil {
			return nil, fmt.Errorf("attach workspace git ref source: %w", err)
		}
		switch ref := attached.(type) {
		case dagql.Result[*GitRef]:
			src.Ref = ref
			return []dagql.AnyResult{ref}, nil
		case dagql.ObjectResult[*GitRef]:
			src.Ref = ref.Result
			return []dagql.AnyResult{ref}, nil
		default:
			return nil, fmt.Errorf("attach workspace git ref source: unexpected result %T", attached)
		}
	case *WorkspaceSourceOverlay:
		var deps []dagql.AnyResult
		baseDeps, err := attachWorkspaceSource(attach, src.Base)
		if err != nil {
			return nil, err
		}
		deps = append(deps, baseDeps...)
		if src.Changes.Self() != nil {
			attached, err := attach(src.Changes)
			if err != nil {
				return nil, fmt.Errorf("attach workspace overlay changes: %w", err)
			}
			changes, ok := attached.(dagql.ObjectResult[*Changeset])
			if !ok {
				return nil, fmt.Errorf("attach workspace overlay changes: unexpected result %T", attached)
			}
			src.Changes = changes
			deps = append(deps, changes)
		}
		return deps, nil
	default:
		return nil, fmt.Errorf("attach workspace source: unsupported source %T", src)
	}
}

func (ws *Workspace) Clone() *Workspace {
	cp := *ws
	return &cp
}

// WorkspaceGit represents the git state associated with a workspace.
type WorkspaceGit struct {
	Workspace dagql.ObjectResult[*Workspace]
}

var _ dagql.HasDependencyResults = (*WorkspaceGit)(nil)

func (*WorkspaceGit) Type() *ast.Type {
	return &ast.Type{
		NamedType: "WorkspaceGit",
		NonNull:   true,
	}
}

func (*WorkspaceGit) TypeDescription() string {
	return "Local git state for a workspace."
}

func (wg *WorkspaceGit) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if wg == nil || wg.Workspace.Self() == nil {
		return nil, nil
	}
	attached, err := attach(wg.Workspace)
	if err != nil {
		return nil, fmt.Errorf("attach workspace git workspace: %w", err)
	}
	typed, ok := attached.(dagql.ObjectResult[*Workspace])
	if !ok {
		return nil, fmt.Errorf("attach workspace git workspace: unexpected result %T", attached)
	}
	wg.Workspace = typed
	return []dagql.AnyResult{typed}, nil
}
