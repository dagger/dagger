package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

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

	// ignorePatterns stores workspace-config ignore patterns, resolved relative
	// to the workspace boundary. Internal only.
	ignorePatterns []string

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
	ClientID string
}

func (*WorkspaceSourceClientLocal) workspaceSource() {}

type WorkspaceSourceDirectory struct {
	Root dagql.ObjectResult[*Directory]
}

func (*WorkspaceSourceDirectory) workspaceSource() {}

type WorkspaceSourceGitRef struct {
	Ref dagql.Result[*GitRef]
}

func (*WorkspaceSourceGitRef) workspaceSource() {}

type WorkspaceSourceOverlay struct {
	Base    WorkspaceSource
	Root    dagql.ObjectResult[*Directory]
	Changes dagql.ObjectResult[*Changeset]
}

func (*WorkspaceSourceOverlay) workspaceSource() {}

func NewWorkspaceSourceClientLocal(hostPath, clientID string) WorkspaceSource {
	return &WorkspaceSourceClientLocal{
		HostPath: hostPath,
		ClientID: clientID,
	}
}

func NewWorkspaceSourceDirectory(root dagql.ObjectResult[*Directory]) WorkspaceSource {
	return &WorkspaceSourceDirectory{
		Root: root,
	}
}

func NewWorkspaceSourceGitRef(ref dagql.Result[*GitRef]) WorkspaceSource {
	return &WorkspaceSourceGitRef{
		Ref: ref,
	}
}

func NewWorkspaceSourceOverlay(base WorkspaceSource, root dagql.ObjectResult[*Directory], changes dagql.ObjectResult[*Changeset]) WorkspaceSource {
	return &WorkspaceSourceOverlay{
		Base:    base,
		Root:    root,
		Changes: changes,
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
		return NewWorkspaceSourceClientLocal(ws.hostPath, ws.ClientID)
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
		if src.Root.Self() != nil {
			return src.Root, true
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

// IgnorePatterns returns workspace-config ignore patterns, resolved relative to
// the workspace boundary.
func (ws *Workspace) IgnorePatterns() []string {
	if ws == nil || len(ws.ignorePatterns) == 0 {
		return nil
	}
	return append([]string(nil), ws.ignorePatterns...)
}

// SetIgnorePatterns sets workspace-config ignore patterns.
func (ws *Workspace) SetIgnorePatterns(ignore []string) {
	if len(ignore) == 0 {
		ws.ignorePatterns = nil
		return
	}
	ws.ignorePatterns = append([]string(nil), ignore...)
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
	IgnorePatterns  []string                      `json:"ignorePatterns,omitempty"`
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
	ChangesID      uint64                    `json:"changesID,omitempty"`
	Base           *persistedWorkspaceSource `json:"base,omitempty"`
}

const (
	persistedWorkspaceSourceClientLocal = "clientLocal"
	persistedWorkspaceSourceDirectory   = "directory"
	persistedWorkspaceSourceGitRef      = "gitRef"
	persistedWorkspaceSourceOverlay     = "overlay"
)

func encodePersistedWorkspaceSource(cache dagql.PersistedObjectCache, src WorkspaceSource) (*persistedWorkspaceSource, error) {
	switch src := src.(type) {
	case *WorkspaceSourceClientLocal:
		return &persistedWorkspaceSource{Kind: persistedWorkspaceSourceClientLocal}, nil
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
		if src.Root.Self() != nil {
			rootID, err := encodePersistedObjectRef(cache, src.Root, "workspace overlay root")
			if err != nil {
				return nil, err
			}
			payload.RootResultID = rootID
		}
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
	clientID string,
) (WorkspaceSource, error) {
	if persisted == nil {
		return nil, nil
	}
	switch persisted.Kind {
	case persistedWorkspaceSourceClientLocal:
		return NewWorkspaceSourceClientLocal(hostPath, clientID), nil
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
		return NewWorkspaceSourceGitRef(ref.Result), nil
	case persistedWorkspaceSourceOverlay:
		base, err := decodePersistedWorkspaceSource(ctx, dag, persisted.Base, rootfs, hostPath, clientID)
		if err != nil {
			return nil, err
		}
		root := rootfs
		if persisted.RootResultID != 0 {
			root, err = loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.RootResultID, "workspace overlay root")
			if err != nil {
				return nil, err
			}
		}
		var changes dagql.ObjectResult[*Changeset]
		if persisted.ChangesID != 0 {
			changes, err = loadPersistedObjectResultByResultID[*Changeset](ctx, dag, persisted.ChangesID, "workspace overlay changes")
			if err != nil {
				return nil, err
			}
		}
		return NewWorkspaceSourceOverlay(base, root, changes), nil
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
		IgnorePatterns:  ws.ignorePatterns,
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
		ignorePatterns:  append([]string(nil), persisted.IgnorePatterns...),
		Address:         persisted.Address,
		Cwd:             cwd,
		ConfigFile:      configFile,
		LockFile:        lockFile,
		ClientID:        persisted.ClientID,
		hostPath:        persisted.HostPath,
	}
	if persisted.Source != nil {
		src, err := decodePersistedWorkspaceSource(ctx, dag, persisted.Source, rootfs, persisted.HostPath, persisted.ClientID)
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
	case nil, *WorkspaceSourceClientLocal:
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
		if src.Root.Self() != nil {
			attached, err := attach(src.Root)
			if err != nil {
				return nil, fmt.Errorf("attach workspace overlay root: %w", err)
			}
			root, ok := attached.(dagql.ObjectResult[*Directory])
			if !ok {
				return nil, fmt.Errorf("attach workspace overlay root: unexpected result %T", attached)
			}
			src.Root = root
			deps = append(deps, root)
		}
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
	cp.ignorePatterns = append([]string(nil), ws.ignorePatterns...)
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
