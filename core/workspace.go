package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"slices"
	"strings"

	workspacepkg "github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/opencontainers/go-digest"
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

// workspaceOverlayModuleLoader resolves the workspace-config modules a
// workspace's pending overlay affects, loading their source through the overlay
// instead of the session's served (on-disk) snapshot. Set by core/schema (which
// owns overlay rootfs resolution); nil in contexts without the workspace
// schema, where overlay module resolution degrades to the served set.
var workspaceOverlayModuleLoader func(context.Context, dagql.ObjectResult[*Workspace]) ([]dagql.ObjectResult[*Module], error)

// SetWorkspaceOverlayModuleLoader registers the hook used to re-resolve
// overlay-affected workspace modules. Mirrors SetWorkspaceInvalidator.
func SetWorkspaceOverlayModuleLoader(fn func(context.Context, dagql.ObjectResult[*Workspace]) ([]dagql.ObjectResult[*Module], error)) {
	workspaceOverlayModuleLoader = fn
}

// WorkspaceOverlayModules re-resolves the workspace-config modules the given
// workspace's pending overlay affects, or nil when there is no overlay (or no
// registered loader). See core/schema's workspaceOverlayModules for semantics.
func WorkspaceOverlayModules(ctx context.Context, ws dagql.ObjectResult[*Workspace]) ([]dagql.ObjectResult[*Module], error) {
	if workspaceOverlayModuleLoader == nil {
		return nil, nil
	}
	return workspaceOverlayModuleLoader(ctx, ws)
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

// workspaceReadEpoch hooks expose the calling client's "workspace read epoch":
// a monotonically bumped token folded into cached host reads (Workspace.file /
// Workspace.directory) so a single long-lived session can invalidate them when
// the workspace's on-disk content changes out from under it.
//
// host.directory reads are cached per client for the client's whole lifetime
// (dagql.PerClientInput), so within one session — such as a `dagger agent`
// conversation — a file read earlier in the session keeps returning its
// original snapshot even after the agent's edits are exported to disk. Bumping
// the epoch on Workspace.export (and on Workspace.reloaded, when an agent
// discards its overlay to re-sync with the host) gives subsequent reads a
// fresh per-client cache namespace, so they re-read the (now updated) host
// instead of the stale snapshot.
//
// Both hooks are registered by engine/server (which owns the per-client cache);
// nil in contexts without a server, where the epoch is empty and bumping is a
// no-op.
var (
	workspaceReadEpochGetter func(context.Context) (string, error)
	workspaceReadEpochBumper func(context.Context) error
)

// SetWorkspaceReadEpochHooks registers the getter/bumper used to scope and
// invalidate a client's cached workspace host reads. Mirrors
// SetWorkspaceInvalidator.
func SetWorkspaceReadEpochHooks(
	get func(context.Context) (string, error),
	bump func(context.Context) error,
) {
	workspaceReadEpochGetter = get
	workspaceReadEpochBumper = bump
}

// WorkspaceReadEpoch returns the calling client's current workspace read epoch,
// or "" when no server has registered the hook (nothing to scope by).
func WorkspaceReadEpoch(ctx context.Context) (string, error) {
	if workspaceReadEpochGetter == nil {
		return "", nil
	}
	return workspaceReadEpochGetter(ctx)
}

// BumpWorkspaceReadEpoch advances the calling client's workspace read epoch so
// cached host reads made before the bump are no longer served. A no-op when no
// server has registered the hook.
func BumpWorkspaceReadEpoch(ctx context.Context) error {
	if workspaceReadEpochBumper == nil {
		return nil
	}
	return workspaceReadEpochBumper(ctx)
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

	// mounts is an in-engine directory tree holding content attached via
	// Workspace.withMountedDirectory/withMountedFile/withMountedCache, keyed by
	// workspace-root-relative mount path. Internal only — not a GraphQL field,
	// but persisted and dependency-tracked like rootfs. Nil when the workspace
	// has no mounts. It is intentionally kept separate from the overlay
	// changeset so mounted content is readable through the normal workspace
	// file tools but is never treated as a pending change or exported to the
	// workspace source.
	mounts dagql.ObjectResult[*Directory]

	// mountPoints lists the workspace-root-relative paths at which content is
	// mounted, sorted and deduplicated. Reads at or under a mount point
	// resolve from mounts (shadowing the source). Overlay edits there are
	// rejected unless the mount is cache-backed (see cacheMounts), keeping
	// mounted Directory/File content read-only.
	mountPoints []string

	// cacheMounts records which mount points are backed by a cache volume (see
	// WorkspaceCacheMount). Cache mounts are the writable kind: edits under
	// them land in mounts rather than the overlay changeset, so they never
	// appear in Workspace.changes, and are committed into the volume on
	// export. Internal only — persisted and dependency-tracked like mounts.
	cacheMounts []WorkspaceCacheMount

	// compatWorkspace stores the originating compat-workspace projection when
	// this workspace was loaded from a legacy dagger.json instead of an explicit
	// dagger.toml. Internal only.
	compatWorkspace *workspacepkg.CompatWorkspace

	// portableCheckpoint distinguishes a frozen value-backed checkout from an
	// ordinary synthetic Directory workspace. Both have no client, but a
	// checkpoint still owns a complete module tree and must compose tools from
	// that tree rather than being treated as an intentionally module-less value.
	portableCheckpoint bool

	// userConfigKey is the normalized Git remote key identifying this
	// workspace in user-level config. Empty when the workspace has no usable
	// remote. Internal only.
	userConfigKey string

	// userConfigOverlay is the user-level config overlay matched for this
	// workspace by userConfigKey. Internal only — user config can carry
	// personal values that must not surface through GraphQL or IDs.
	userConfigOverlay *workspacepkg.UserWorkspaceOverlay

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

	// StagedGeneration records the workspace-root-relative paths of modules
	// whose generated local dependency closure has been applied to this
	// workspace, via the internal Workspace.__withGeneratedLocalDependencies
	// field. Nested local-dependency generation for a
	// recorded module short-circuits to an empty changeset — without this, a
	// dependency's SDK generator re-stages its own dependency closure and
	// generation fans out exponentially over the dependency DAG. Kept sorted
	// and deduplicated; carried through Clone so derived workspaces keep it.
	StagedGeneration []string

	// GitAuthorName and GitAuthorEmail record the git identity observed on the
	// client at workspace load time (user.name / user.email). They are read
	// once, here, so that engine-side commits (Workspace.withCommit) are
	// hermetic: nothing consults the client's git config per call.
	GitAuthorName  string
	GitAuthorEmail string

	// BaseHeadSHA is the commit the workspace's git HEAD resolved to before any
	// pending commit was staged. Recorded when the first pending commit is
	// staged, so a later export can check that the local checkout has not moved
	// out from under the staged stack.
	BaseHeadSHA string

	// pendingCommits is the stack of commits staged engine-side by
	// Workspace.withCommit. They exist only in the engine: the user's checkout
	// is untouched until the stack is exported. Internal only — not a GraphQL
	// field, but persisted and dependency-tracked like mounts.
	pendingCommits []WorkspacePendingCommit
}

// Workspace checkpoint payloads are recipe values, not storage handles. Keep
// every leaf and the complete reconstruction bounded before allocating or
// touching a snapshot. The chunk size is deliberately conservative; capture may
// split more finely, but restore never accepts a leaf large enough to become an
// unbounded trace frame.
const (
	WorkspaceCheckpointFormatVersion = 1

	workspaceCheckpointMaxManifestBytes = 1 << 20
	workspaceCheckpointMaxChunkBytes    = 1 << 20
	workspaceCheckpointMaxChunks        = 1024
	workspaceCheckpointMaxPayloadBytes  = 256 << 20
)

// WorkspaceCheckpointChunk is one opaque, content-addressed leaf of a portable
// workspace recipe. Its bytes are intentionally not GraphQL fields: the only
// operation that consumes them is the internal checkpoint constructor.
type WorkspaceCheckpointChunk struct {
	data   []byte
	digest digest.Digest
}

func (*WorkspaceCheckpointChunk) Type() *ast.Type {
	return &ast.Type{NamedType: "WorkspaceCheckpointChunk", NonNull: true}
}

func (*WorkspaceCheckpointChunk) TypeDescription() string {
	return "An internal bounded payload chunk used to reconstruct a workspace checkpoint."
}

func NewWorkspaceCheckpointChunk(data []byte, claimed digest.Digest) (*WorkspaceCheckpointChunk, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("workspace checkpoint chunk is empty")
	}
	if len(data) > workspaceCheckpointMaxChunkBytes {
		return nil, fmt.Errorf("workspace checkpoint chunk is %d bytes; maximum is %d", len(data), workspaceCheckpointMaxChunkBytes)
	}
	actual := digest.FromBytes(data)
	if claimed != "" && claimed != actual {
		return nil, fmt.Errorf("workspace checkpoint chunk digest %s does not match content %s", claimed, actual)
	}
	return &WorkspaceCheckpointChunk{data: slices.Clone(data), digest: actual}, nil
}

func (chunk *WorkspaceCheckpointChunk) Data() []byte {
	if chunk == nil {
		return nil
	}
	return slices.Clone(chunk.data)
}

func (chunk *WorkspaceCheckpointChunk) Digest() digest.Digest {
	if chunk == nil {
		return ""
	}
	return chunk.digest
}

// WorkspaceGitCheckpointManifest is the complete, versioned contract for the
// pure checkpoint constructor. Payload chunks are listed in bundle-then-
// worktree order; each descriptor binds an argument position to exact bytes.
type WorkspaceGitCheckpointManifest struct {
	Version              int                          `json:"version"`
	ObjectFormat         string                       `json:"objectFormat"`
	BaseSHA              string                       `json:"baseSHA"`
	HeadSHA              string                       `json:"headSHA"`
	BundleRef            string                       `json:"bundleRef,omitempty"`
	Bundle               WorkspaceCheckpointPayload   `json:"bundle"`
	Worktree             WorkspaceCheckpointPayload   `json:"worktree"`
	WorktreeTree         string                       `json:"worktreeTree"`
	Commits              []WorkspaceBundledCommit     `json:"commits"`
	Workspace            WorkspaceCheckpointWorkspace `json:"workspace"`
	CapturePolicyVersion string                       `json:"capturePolicyVersion"`
}

type WorkspaceCheckpointPayload struct {
	Size   int64                                `json:"size"`
	Digest string                               `json:"digest"`
	Chunks []WorkspaceCheckpointChunkDescriptor `json:"chunks"`
}

type WorkspaceCheckpointChunkDescriptor struct {
	Size   int    `json:"size"`
	Digest string `json:"digest"`
}

type WorkspaceBundledCommit struct {
	SHA         string   `json:"sha"`
	Origin      string   `json:"origin,omitempty"`
	Message     string   `json:"message"`
	Date        string   `json:"date"`
	AuthorName  string   `json:"authorName"`
	AuthorEmail string   `json:"authorEmail"`
	Paths       []string `json:"paths"`
}

type WorkspaceCheckpointWorkspace struct {
	Address        string `json:"address,omitempty"`
	Cwd            string `json:"cwd"`
	ConfigFile     string `json:"configFile,omitempty"`
	LockFile       string `json:"lockFile,omitempty"`
	GitAuthorName  string `json:"gitAuthorName,omitempty"`
	GitAuthorEmail string `json:"gitAuthorEmail,omitempty"`
}

// ParseWorkspaceGitCheckpointManifest rejects unknown fields and trailing data
// so a newer producer cannot be silently interpreted with older semantics.
func ParseWorkspaceGitCheckpointManifest(raw string) (*WorkspaceGitCheckpointManifest, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("workspace checkpoint manifest is empty")
	}
	if len(raw) > workspaceCheckpointMaxManifestBytes {
		return nil, fmt.Errorf("workspace checkpoint manifest is %d bytes; maximum is %d", len(raw), workspaceCheckpointMaxManifestBytes)
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var manifest WorkspaceGitCheckpointManifest
	if err := dec.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode workspace checkpoint manifest: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode workspace checkpoint manifest: trailing JSON value")
		}
		return nil, fmt.Errorf("decode workspace checkpoint manifest: %w", err)
	}
	if manifest.Version != WorkspaceCheckpointFormatVersion {
		return nil, fmt.Errorf("unsupported workspace checkpoint format version %d (expected %d)", manifest.Version, WorkspaceCheckpointFormatVersion)
	}
	return &manifest, nil
}

// AssembleWorkspaceCheckpointPayloads verifies positional chunk descriptors,
// per-payload digests and all aggregate bounds before returning the two byte
// streams consumed by Git reconstruction.
func AssembleWorkspaceCheckpointPayloads(
	manifest *WorkspaceGitCheckpointManifest,
	chunks []*WorkspaceCheckpointChunk,
) (bundle, worktree []byte, _ error) {
	if manifest == nil {
		return nil, nil, fmt.Errorf("workspace checkpoint manifest is required")
	}
	if len(chunks) > workspaceCheckpointMaxChunks {
		return nil, nil, fmt.Errorf("workspace checkpoint has %d chunks; maximum is %d", len(chunks), workspaceCheckpointMaxChunks)
	}
	total := manifest.Bundle.Size + manifest.Worktree.Size
	if manifest.Bundle.Size < 0 || manifest.Worktree.Size < 0 || total < 0 || total > workspaceCheckpointMaxPayloadBytes {
		return nil, nil, fmt.Errorf("workspace checkpoint payload is %d bytes; maximum is %d", total, workspaceCheckpointMaxPayloadBytes)
	}

	pos := 0
	assemble := func(name string, payload WorkspaceCheckpointPayload) ([]byte, error) {
		if payload.Size > workspaceCheckpointMaxPayloadBytes {
			return nil, fmt.Errorf("workspace checkpoint %s payload is %d bytes; maximum is %d", name, payload.Size, workspaceCheckpointMaxPayloadBytes)
		}
		buf := bytes.NewBuffer(make([]byte, 0, int(payload.Size)))
		for i, descriptor := range payload.Chunks {
			if pos >= len(chunks) {
				return nil, fmt.Errorf("workspace checkpoint %s chunk %d is missing", name, i)
			}
			chunk := chunks[pos]
			pos++
			if chunk == nil {
				return nil, fmt.Errorf("workspace checkpoint %s chunk %d is nil", name, i)
			}
			if descriptor.Size <= 0 || descriptor.Size > workspaceCheckpointMaxChunkBytes {
				return nil, fmt.Errorf("workspace checkpoint %s chunk %d has invalid size %d", name, i, descriptor.Size)
			}
			if descriptor.Size != len(chunk.data) {
				return nil, fmt.Errorf("workspace checkpoint %s chunk %d size is %d, expected %d", name, i, len(chunk.data), descriptor.Size)
			}
			expected, err := parseWorkspaceCheckpointDigest(descriptor.Digest, fmt.Sprintf("%s chunk %d", name, i))
			if err != nil {
				return nil, err
			}
			if expected != chunk.digest {
				return nil, fmt.Errorf("workspace checkpoint %s chunk %d digest is %s, expected %s", name, i, chunk.digest, expected)
			}
			_, _ = buf.Write(chunk.data)
		}
		if int64(buf.Len()) != payload.Size {
			return nil, fmt.Errorf("workspace checkpoint %s payload size is %d, expected %d", name, buf.Len(), payload.Size)
		}
		expected, err := parseWorkspaceCheckpointDigest(payload.Digest, name+" payload")
		if err != nil {
			return nil, err
		}
		actual := digest.FromBytes(buf.Bytes())
		if actual != expected {
			return nil, fmt.Errorf("workspace checkpoint %s payload digest is %s, expected %s", name, actual, expected)
		}
		return buf.Bytes(), nil
	}

	var err error
	bundle, err = assemble("bundle", manifest.Bundle)
	if err != nil {
		return nil, nil, err
	}
	worktree, err = assemble("worktree", manifest.Worktree)
	if err != nil {
		return nil, nil, err
	}
	if pos != len(chunks) {
		return nil, nil, fmt.Errorf("workspace checkpoint has %d unreferenced chunks", len(chunks)-pos)
	}
	return bundle, worktree, nil
}

func parseWorkspaceCheckpointDigest(raw, label string) (digest.Digest, error) {
	parsed, err := digest.Parse(raw)
	if err != nil || parsed.Algorithm() != digest.SHA256 {
		return "", fmt.Errorf("workspace checkpoint %s has invalid sha256 digest %q", label, raw)
	}
	if err := parsed.Validate(); err != nil {
		return "", fmt.Errorf("workspace checkpoint %s has invalid digest %q: %w", label, raw, err)
	}
	return parsed, nil
}

// WorkspacePendingCommit is one commit staged engine-side on top of the
// workspace's base HEAD (and any previously staged commits), without touching
// the user's local checkout.
type WorkspacePendingCommit struct {
	// SHA is the resulting commit hash.
	SHA string
	// Origin is the hash of the commit this one was replayed from, when it was
	// pulled out of another workspace by Workspace.withCommitsFrom. Empty for
	// commits authored in this workspace by Workspace.withCommit.
	//
	// It collapses transitively to the root: replaying a commit that already
	// carries an origin records THAT origin, not the immediate source's hash.
	// So a commit pulled A -> B -> C still names the commit A staged, and a
	// later pull straight from A recognises it as already present.
	Origin string
	// Message is the commit message.
	Message string
	// Date is the RFC3339 author *and* committer date the commit was made with.
	Date string
	// AuthorName and AuthorEmail are the identity the commit was made with.
	AuthorName  string
	AuthorEmail string
	// Paths is the path scope the commit was made with, empty for "everything
	// uncommitted at the time".
	Paths []string
	// Repo is the repository tree whose .git holds this commit (and every
	// commit staged before it).
	Repo dagql.ObjectResult[*Directory]
	// Committed is the cumulative content of every commit staged so far,
	// expressed as a changeset from the workspace overlay's base to the staged
	// state. Staging a commit never changes what the workspace tree contains —
	// the overlay keeps carrying every edit — so this is what tells the diff
	// views (Workspace.changes, WorkspaceGit.uncommitted) which part of the
	// overlay is already committed: they diff the overlay's "after" tree
	// against base+Committed, leaving exactly the uncommitted remainder.
	//
	// Unset for workspaces whose pending changes come from the repository
	// rather than an overlay: those read their remainder from git directly.
	Committed dagql.ObjectResult[*Changeset]
}

// PendingCommits returns the stack of engine-side staged commits, oldest first.
func (ws *Workspace) PendingCommits() []WorkspacePendingCommit {
	if ws == nil {
		return nil
	}
	return ws.pendingCommits
}

// LatestPendingCommit returns the newest staged commit, if any.
func (ws *Workspace) LatestPendingCommit() (WorkspacePendingCommit, bool) {
	if ws == nil || len(ws.pendingCommits) == 0 {
		return WorkspacePendingCommit{}, false
	}
	return ws.pendingCommits[len(ws.pendingCommits)-1], true
}

// StagedChanges returns the cumulative content of the staged commits, as a
// changeset anchored at the workspace overlay's base. Applying it to that base
// yields the staged tree — the "HEAD" the uncommitted views diff against.
func (ws *Workspace) StagedChanges() (dagql.ObjectResult[*Changeset], bool) {
	latest, ok := ws.LatestPendingCommit()
	if !ok || latest.Committed.Self() == nil {
		return dagql.ObjectResult[*Changeset]{}, false
	}
	return latest.Committed, true
}

// WithPendingCommit returns a clone of the workspace with the given commit
// pushed onto the pending commit stack. The slice is copied so the clone never
// aliases the parent's backing array.
func (ws *Workspace) WithPendingCommit(c WorkspacePendingCommit) *Workspace {
	cp := ws.Clone()
	commits := make([]WorkspacePendingCommit, 0, len(ws.pendingCommits)+1)
	commits = append(commits, ws.pendingCommits...)
	commits = append(commits, c)
	cp.pendingCommits = commits
	return cp
}

// WorkspaceCacheMount is a CacheVolume mounted into the workspace's mounts tree
// at Target. It shadows base workspace content there and stays out of
// Workspace.changes, like any other mount — but unlike a mounted Directory or
// File it is writable: edits under Target update the mounts tree, and export
// commits the resulting delta back into the volume.
type WorkspaceCacheMount struct {
	// Target is the workspace-root-relative mount path, cleaned, no leading
	// slash (e.g. "foo", "build/cache"). It is also the key into the mounts
	// tree, so the mount's current content is mounts.directory(Target).
	Target string
	// Volume is the cache volume backing this mount.
	Volume dagql.ObjectResult[*CacheVolume]
	// Baseline is the volume's content as of the mount — what the mounts tree
	// held at Target before any edits. Export diffs the mount's current subtree
	// against it, so only edits made through this workspace are written back and
	// content another writer added to the volume meanwhile is left alone. It is
	// a lazy Directory, so an untouched mount never materializes the volume.
	Baseline dagql.ObjectResult[*Directory]
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
	//
	// Every entry is either a path the delta root now owns in full, or a member
	// of RemovedPaths. An entry that is neither — a directory the delta root
	// holds only part of — silently turns every host file beneath it into a
	// deletion, since the sparse base carries the whole subtree (an include
	// pattern matching a directory matches everything under it) while the delta
	// root does not. See RemovedPaths.
	TouchedPaths []string
	// RemovedPaths is the subset of TouchedPaths an edit deliberately deleted:
	// the only paths that may legitimately be present in the sparse base and
	// absent from the delta root, and therefore the only removals the overlay's
	// changeset is allowed to report.
	//
	// It is the intent ledger the removal guard checks the computed changeset
	// against (assertOverlayRemovalsIntended), so a bookkeeping mistake that
	// widens the base surfaces as a loud refusal instead of a silent mass
	// deletion of the user's tree.
	RemovedPaths []string
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
	removedPaths []string,
	changes dagql.ObjectResult[*Changeset],
) WorkspaceSource {
	if overlay, ok := base.(*WorkspaceSourceOverlay); ok {
		base = overlay.Base
	}
	// The caller accumulates TouchedPaths and RemovedPaths (union with the
	// parent overlay's) before constructing, so they are already cumulative
	// here.
	return &WorkspaceSourceOverlay{
		Base:         base,
		TouchedPaths: touchedPaths,
		RemovedPaths: removedPaths,
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

// OverlayRemovedPaths returns the cumulative set of workspace-relative paths
// the overlay's edits deliberately removed — the only removals its changeset is
// allowed to report.
func (ws *Workspace) OverlayRemovedPaths() []string {
	overlay, ok := ws.Source().(*WorkspaceSourceOverlay)
	if !ok {
		return nil
	}
	return overlay.RemovedPaths
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

func (ws *Workspace) IsPortableCheckpoint() bool {
	return ws != nil && ws.portableCheckpoint
}

func (ws *Workspace) SetPortableCheckpoint() {
	if ws != nil {
		ws.portableCheckpoint = true
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

func (ws *Workspace) UserConfigKey() string {
	if ws == nil {
		return ""
	}
	return ws.userConfigKey
}

func (ws *Workspace) SetUserConfigKey(key string) {
	ws.userConfigKey = key
}

func (ws *Workspace) UserConfigOverlay() *workspacepkg.UserWorkspaceOverlay {
	if ws == nil {
		return nil
	}
	return ws.userConfigOverlay
}

func (ws *Workspace) SetUserConfigOverlay(overlay *workspacepkg.UserWorkspaceOverlay) {
	ws.userConfigOverlay = overlay
}

// MountsDir returns the directory tree holding mounted content, keyed by
// workspace-root-relative mount path, or false when the workspace has no
// mounts.
func (ws *Workspace) MountsDir() (dagql.ObjectResult[*Directory], bool) {
	if ws == nil || ws.mounts.Self() == nil {
		return dagql.ObjectResult[*Directory]{}, false
	}
	return ws.mounts, true
}

// MountPoints returns the workspace-root-relative paths at which content is
// mounted, sorted and deduplicated. The returned slice is shared with the
// workspace and must not be mutated.
func (ws *Workspace) MountPoints() []string {
	if ws == nil {
		return nil
	}
	return ws.mountPoints
}

// WithMounted returns a copy of the workspace with the given mounted content
// tree and the workspace-root-relative path recorded as a mount point, keeping
// the mount point list sorted and deduplicated.
func (ws *Workspace) WithMounted(newMounts dagql.ObjectResult[*Directory], path string) *Workspace {
	cp := ws.Clone()
	cp.mounts = newMounts
	p := filepath.ToSlash(path)
	if i, found := slices.BinarySearch(cp.mountPoints, p); !found {
		cp.mountPoints = slices.Insert(cp.mountPoints, i, p)
	}
	return cp
}

// WithMountsDir returns a copy of the workspace with an updated mounts tree and
// the mount points left as they are. Used to apply an edit under a cache mount,
// which changes mounted content without mounting anything new.
func (ws *Workspace) WithMountsDir(newMounts dagql.ObjectResult[*Directory]) *Workspace {
	cp := ws.Clone()
	cp.mounts = newMounts
	return cp
}

// WithCacheMounted returns a copy of the workspace with the given mounted
// content tree, the mount recorded as a cache-backed (writable) mount point,
// and any mount previously covering the same target replaced.
func (ws *Workspace) WithCacheMounted(newMounts dagql.ObjectResult[*Directory], mount WorkspaceCacheMount) *Workspace {
	cp := ws.WithMounted(newMounts, mount.Target)
	cacheMounts := make([]WorkspaceCacheMount, 0, len(cp.cacheMounts)+1)
	for _, existing := range cp.cacheMounts {
		if existing.Target != mount.Target {
			cacheMounts = append(cacheMounts, existing)
		}
	}
	cacheMounts = append(cacheMounts, mount)
	slices.SortFunc(cacheMounts, func(a, b WorkspaceCacheMount) int {
		return strings.Compare(a.Target, b.Target)
	})
	cp.cacheMounts = cacheMounts
	return cp
}

// WithoutMountedAt returns a copy of the workspace with the given mounts tree
// and every mount point at or under the workspace-root-relative path dropped —
// unmounting a directory unmounts whatever was mounted inside it.
func (ws *Workspace) WithoutMountedAt(newMounts dagql.ObjectResult[*Directory], path string) *Workspace {
	cp := ws.Clone()
	cp.mounts = newMounts
	p := filepath.ToSlash(path)
	covered := func(target string) bool {
		return target == p || strings.HasPrefix(target, p+"/")
	}
	cp.mountPoints = slices.DeleteFunc(cp.mountPoints, covered)
	cp.cacheMounts = slices.DeleteFunc(cp.cacheMounts, func(m WorkspaceCacheMount) bool {
		return covered(m.Target)
	})
	return cp
}

// CacheMounts returns the workspace's cache-backed mounts (see
// WorkspaceCacheMount).
func (ws *Workspace) CacheMounts() []WorkspaceCacheMount {
	if ws == nil {
		return nil
	}
	return ws.cacheMounts
}

// CacheMountForPath returns the cache mount a workspace-root-relative path may
// be edited through: the deepest mount point covering it, when that mount is
// cache-backed. A Directory or File mount is read-only, and one nested under a
// cache mount keeps its own subtree read-only.
func (ws *Workspace) CacheMountForPath(resolvedPath string) (WorkspaceCacheMount, bool) {
	mp, ok := ws.deepestMountPoint(resolvedPath)
	if !ok {
		return WorkspaceCacheMount{}, false
	}
	for _, m := range ws.cacheMounts {
		if m.Target == mp {
			return m, true
		}
	}
	return WorkspaceCacheMount{}, false
}

// deepestMountPoint returns the longest mount point covering a
// workspace-root-relative path, so a nested mount shadows the one it sits in.
func (ws *Workspace) deepestMountPoint(resolvedPath string) (string, bool) {
	if ws == nil {
		return "", false
	}
	p := filepath.ToSlash(resolvedPath)
	var (
		best  string
		found bool
	)
	for _, mp := range ws.mountPoints {
		if p != mp && !strings.HasPrefix(p, mp+"/") {
			continue
		}
		if !found || len(mp) > len(best) {
			best = mp
			found = true
		}
	}
	return best, found
}

// MountedPath reports whether a workspace-root-relative path is at or under
// one of the workspace's mount points.
func (ws *Workspace) MountedPath(resolvedPath string) bool {
	if ws == nil {
		return false
	}
	p := filepath.ToSlash(resolvedPath)
	for _, mp := range ws.mountPoints {
		if p == mp || strings.HasPrefix(p, mp+"/") {
			return true
		}
	}
	return false
}

// HasMountsUnder reports whether any mount point lies strictly below the given
// workspace-root-relative path.
func (ws *Workspace) HasMountsUnder(resolvedPath string) bool {
	if ws == nil || len(ws.mountPoints) == 0 {
		return false
	}
	p := filepath.ToSlash(resolvedPath)
	if p == "." || p == "" {
		return true
	}
	for _, mp := range ws.mountPoints {
		if strings.HasPrefix(mp, p+"/") {
			return true
		}
	}
	return false
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
	RootfsResultID     uint64                         `json:"rootfsResultID,omitempty"`
	MountsResultID     uint64                         `json:"mountsResultID,omitempty"`
	MountPoints        []string                       `json:"mountPoints,omitempty"`
	CacheMounts        []persistedWorkspaceCacheMount `json:"cacheMounts,omitempty"`
	Source             *persistedWorkspaceSource      `json:"source,omitempty"`
	CompatWorkspace    *workspacepkg.CompatWorkspace  `json:"compatWorkspace,omitempty"`
	PortableCheckpoint bool                           `json:"portableCheckpoint,omitempty"`
	Address            string                         `json:"address,omitempty"`
	Cwd                string                         `json:"cwd,omitempty"`
	ConfigFile         string                         `json:"configFile,omitempty"`
	LockFile           string                         `json:"lockFile,omitempty"`
	ClientID           string                         `json:"clientID,omitempty"`
	HostPath           string                         `json:"hostPath,omitempty"`

	StagedGeneration []string `json:"stagedGeneration,omitempty"`

	GitAuthorName  string `json:"gitAuthorName,omitempty"`
	GitAuthorEmail string `json:"gitAuthorEmail,omitempty"`
	BaseHeadSHA    string `json:"baseHeadSHA,omitempty"`

	PendingCommits []persistedWorkspacePendingCommit `json:"pendingCommits,omitempty"`

	// Decode-only names from main's pre-workspace-selection payload.
	LegacyPath       string `json:"path,omitempty"`
	LegacyConfigPath string `json:"configPath,omitempty"`
}

// persistedWorkspaceCacheMount is the on-disk encoding of a WorkspaceCacheMount:
// its Target plus references to the backing volume and the baseline export
// diffs against. The mounted content itself lives in the mounts tree, which is
// persisted separately.
type persistedWorkspaceCacheMount struct {
	Target           string `json:"target,omitempty"`
	VolumeResultID   uint64 `json:"volumeResultID,omitempty"`
	BaselineResultID uint64 `json:"baselineResultID,omitempty"`
}

// persistedWorkspacePendingCommit is the on-disk encoding of a
// WorkspacePendingCommit: its metadata plus a reference to the repository tree
// holding the commit.
type persistedWorkspacePendingCommit struct {
	SHA          string   `json:"sha,omitempty"`
	Origin       string   `json:"origin,omitempty"`
	Message      string   `json:"message,omitempty"`
	Date         string   `json:"date,omitempty"`
	AuthorName   string   `json:"authorName,omitempty"`
	AuthorEmail  string   `json:"authorEmail,omitempty"`
	Paths        []string `json:"paths,omitempty"`
	RepoResultID uint64   `json:"repoResultID,omitempty"`
	CommittedID  uint64   `json:"committedID,omitempty"`
}

type persistedWorkspaceSource struct {
	Kind           string                    `json:"kind"`
	RootResultID   uint64                    `json:"rootResultID,omitempty"`
	GitRefResultID uint64                    `json:"gitRefResultID,omitempty"`
	ExplicitCommit bool                      `json:"explicitCommit,omitempty"`
	ChangesID      uint64                    `json:"changesID,omitempty"`
	TouchedPaths   []string                  `json:"touchedPaths,omitempty"`
	RemovedPaths   []string                  `json:"removedPaths,omitempty"`
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
		payload.RemovedPaths = src.RemovedPaths
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
		return NewWorkspaceSourceOverlay(base, persisted.TouchedPaths, persisted.RemovedPaths, changes), nil
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
		CompatWorkspace:    ws.compatWorkspace,
		PortableCheckpoint: ws.portableCheckpoint,
		Address:            ws.Address,
		Cwd:                ws.Cwd,
		ConfigFile:         ws.ConfigFile,
		LockFile:           ws.LockFile,
		ClientID:           ws.ClientID,
		HostPath:           ws.hostPath,
		StagedGeneration:   ws.StagedGeneration,
		GitAuthorName:      ws.GitAuthorName,
		GitAuthorEmail:     ws.GitAuthorEmail,
		BaseHeadSHA:        ws.BaseHeadSHA,
	}
	if ws.rootfs.Self() != nil {
		rootfsID, err := encodePersistedObjectRef(cache, ws.rootfs, "workspace rootfs")
		if err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
		payload.RootfsResultID = rootfsID
	}
	if ws.mounts.Self() != nil {
		mountsID, err := encodePersistedObjectRef(cache, ws.mounts, "workspace mounts")
		if err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
		payload.MountsResultID = mountsID
		payload.MountPoints = ws.mountPoints
	}
	if ws.Source() != nil {
		source, err := encodePersistedWorkspaceSource(cache, ws.Source())
		if err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
		payload.Source = source
	}
	for _, m := range ws.cacheMounts {
		persistedMount := persistedWorkspaceCacheMount{Target: m.Target}
		if m.Volume.Self() != nil {
			volumeID, err := encodePersistedObjectRef(cache, m.Volume, "workspace cache mount volume")
			if err != nil {
				return dagql.PersistedObjectEncoding{}, err
			}
			persistedMount.VolumeResultID = volumeID
		}
		if m.Baseline.Self() != nil {
			baselineID, err := encodePersistedObjectRef(cache, m.Baseline, "workspace cache mount baseline")
			if err != nil {
				return dagql.PersistedObjectEncoding{}, err
			}
			persistedMount.BaselineResultID = baselineID
		}
		payload.CacheMounts = append(payload.CacheMounts, persistedMount)
	}
	for _, c := range ws.pendingCommits {
		persistedCommit := persistedWorkspacePendingCommit{
			SHA:         c.SHA,
			Origin:      c.Origin,
			Message:     c.Message,
			Date:        c.Date,
			AuthorName:  c.AuthorName,
			AuthorEmail: c.AuthorEmail,
			Paths:       c.Paths,
		}
		if c.Repo.Self() != nil {
			repoID, err := encodePersistedObjectRef(cache, c.Repo, "workspace pending commit repo")
			if err != nil {
				return dagql.PersistedObjectEncoding{}, err
			}
			persistedCommit.RepoResultID = repoID
		}
		if c.Committed.Self() != nil {
			committedID, err := encodePersistedObjectRef(cache, c.Committed, "workspace pending commit committed changes")
			if err != nil {
				return dagql.PersistedObjectEncoding{}, err
			}
			persistedCommit.CommittedID = committedID
		}
		payload.PendingCommits = append(payload.PendingCommits, persistedCommit)
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

	var mounts dagql.ObjectResult[*Directory]
	if persisted.MountsResultID != 0 {
		var err error
		mounts, err = loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.MountsResultID, "workspace mounts")
		if err != nil {
			return nil, err
		}
	}

	var cacheMounts []WorkspaceCacheMount
	for _, persistedMount := range persisted.CacheMounts {
		mount := WorkspaceCacheMount{Target: persistedMount.Target}
		if persistedMount.VolumeResultID != 0 {
			volume, err := loadPersistedObjectResultByResultID[*CacheVolume](ctx, dag, persistedMount.VolumeResultID, "workspace cache mount volume")
			if err != nil {
				return nil, err
			}
			mount.Volume = volume
		}
		if persistedMount.BaselineResultID != 0 {
			baseline, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persistedMount.BaselineResultID, "workspace cache mount baseline")
			if err != nil {
				return nil, err
			}
			mount.Baseline = baseline
		}
		cacheMounts = append(cacheMounts, mount)
	}

	var pendingCommits []WorkspacePendingCommit
	for _, persistedCommit := range persisted.PendingCommits {
		commit := WorkspacePendingCommit{
			SHA:         persistedCommit.SHA,
			Origin:      persistedCommit.Origin,
			Message:     persistedCommit.Message,
			Date:        persistedCommit.Date,
			AuthorName:  persistedCommit.AuthorName,
			AuthorEmail: persistedCommit.AuthorEmail,
			Paths:       persistedCommit.Paths,
		}
		if persistedCommit.RepoResultID != 0 {
			repo, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persistedCommit.RepoResultID, "workspace pending commit repo")
			if err != nil {
				return nil, err
			}
			commit.Repo = repo
		}
		if persistedCommit.CommittedID != 0 {
			committed, err := loadPersistedObjectResultByResultID[*Changeset](ctx, dag, persistedCommit.CommittedID, "workspace pending commit committed changes")
			if err != nil {
				return nil, err
			}
			commit.Committed = committed
		}
		pendingCommits = append(pendingCommits, commit)
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
		rootfs:             rootfs,
		mounts:             mounts,
		mountPoints:        persisted.MountPoints,
		cacheMounts:        cacheMounts,
		compatWorkspace:    persisted.CompatWorkspace,
		portableCheckpoint: persisted.PortableCheckpoint,
		Address:            persisted.Address,
		Cwd:                cwd,
		ConfigFile:         configFile,
		LockFile:           lockFile,
		ClientID:           persisted.ClientID,
		hostPath:           persisted.HostPath,
		StagedGeneration:   persisted.StagedGeneration,
		GitAuthorName:      persisted.GitAuthorName,
		GitAuthorEmail:     persisted.GitAuthorEmail,
		BaseHeadSHA:        persisted.BaseHeadSHA,
		pendingCommits:     pendingCommits,
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
	if ws == nil {
		return nil, nil
	}

	var deps []dagql.AnyResult

	if ws.rootfs.Self() != nil {
		attached, err := attach(ws.rootfs)
		if err != nil {
			return nil, fmt.Errorf("attach workspace rootfs: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*Directory])
		if !ok {
			return nil, fmt.Errorf("attach workspace rootfs: unexpected result %T", attached)
		}
		ws.rootfs = typed
		deps = append(deps, typed)
	}

	if ws.source != nil {
		sourceDeps, err := attachWorkspaceSource(attach, ws.source)
		if err != nil {
			return nil, err
		}
		deps = append(deps, sourceDeps...)
	}

	if ws.mounts.Self() != nil {
		attached, err := attach(ws.mounts)
		if err != nil {
			return nil, fmt.Errorf("attach workspace mounts: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*Directory])
		if !ok {
			return nil, fmt.Errorf("attach workspace mounts: unexpected result %T", attached)
		}
		ws.mounts = typed
		deps = append(deps, typed)
	}

	for i := range ws.cacheMounts {
		if ws.cacheMounts[i].Volume.Self() != nil {
			attached, err := attach(ws.cacheMounts[i].Volume)
			if err != nil {
				return nil, fmt.Errorf("attach workspace cache mount volume: %w", err)
			}
			typed, ok := attached.(dagql.ObjectResult[*CacheVolume])
			if !ok {
				return nil, fmt.Errorf("attach workspace cache mount volume: unexpected result %T", attached)
			}
			ws.cacheMounts[i].Volume = typed
			deps = append(deps, typed)
		}
		if ws.cacheMounts[i].Baseline.Self() != nil {
			attached, err := attach(ws.cacheMounts[i].Baseline)
			if err != nil {
				return nil, fmt.Errorf("attach workspace cache mount baseline: %w", err)
			}
			typed, ok := attached.(dagql.ObjectResult[*Directory])
			if !ok {
				return nil, fmt.Errorf("attach workspace cache mount baseline: unexpected result %T", attached)
			}
			ws.cacheMounts[i].Baseline = typed
			deps = append(deps, typed)
		}
	}

	for i := range ws.pendingCommits {
		if ws.pendingCommits[i].Repo.Self() != nil {
			attached, err := attach(ws.pendingCommits[i].Repo)
			if err != nil {
				return nil, fmt.Errorf("attach workspace pending commit repo: %w", err)
			}
			typed, ok := attached.(dagql.ObjectResult[*Directory])
			if !ok {
				return nil, fmt.Errorf("attach workspace pending commit repo: unexpected result %T", attached)
			}
			ws.pendingCommits[i].Repo = typed
			deps = append(deps, typed)
		}
		if ws.pendingCommits[i].Committed.Self() != nil {
			attached, err := attach(ws.pendingCommits[i].Committed)
			if err != nil {
				return nil, fmt.Errorf("attach workspace pending commit committed changes: %w", err)
			}
			typed, ok := attached.(dagql.ObjectResult[*Changeset])
			if !ok {
				return nil, fmt.Errorf("attach workspace pending commit committed changes: unexpected result %T", attached)
			}
			ws.pendingCommits[i].Committed = typed
			deps = append(deps, typed)
		}
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
	cp.mountPoints = slices.Clone(ws.mountPoints)
	cp.cacheMounts = slices.Clone(ws.cacheMounts)
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

var (
	_ dagql.PersistedObject        = (*WorkspaceGit)(nil)
	_ dagql.PersistedObjectDecoder = (*WorkspaceGit)(nil)
)

// persistedWorkspaceGitPayload is the on-disk encoding of a WorkspaceGit. Its
// only state is a reference to the Workspace it wraps, which is itself
// persistable, so persistence reduces to encoding that one ref.
type persistedWorkspaceGitPayload struct {
	WorkspaceResultID uint64 `json:"workspaceResultID,omitempty"`
}

func (wg *WorkspaceGit) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	if wg == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted workspace git: nil workspace git")
	}
	var payload persistedWorkspaceGitPayload
	if wg.Workspace.Self() != nil {
		wsID, err := encodePersistedObjectRef(cache, wg.Workspace, "workspace git workspace")
		if err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
		payload.WorkspaceResultID = wsID
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("marshal persisted workspace git payload: %w", err)
	}
	return encodePersistedObjectRawJSON(payloadBytes), nil
}

func (*WorkspaceGit) DecodePersistedObject(
	ctx context.Context,
	dag *dagql.Server,
	_ uint64,
	_ *dagql.ResultCall,
	payload json.RawMessage,
) (dagql.Typed, error) {
	var persisted persistedWorkspaceGitPayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted workspace git payload: %w", err)
	}
	wg := &WorkspaceGit{}
	if persisted.WorkspaceResultID != 0 {
		ws, err := loadPersistedObjectResultByResultID[*Workspace](ctx, dag, persisted.WorkspaceResultID, "workspace git workspace")
		if err != nil {
			return nil, err
		}
		wg.Workspace = ws
	}
	return wg, nil
}
