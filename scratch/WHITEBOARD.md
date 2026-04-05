# WHITEBOARD

## TODO
* Remove `Module.persistedResultID` and stop mutating typed `*Module` self values with attached result IDs
  * The remaining read is only for the `IncludeSelfInDeps` skip during `Module.EncodePersistedObject`
  * Follow up by hard-cutting the persisted-object encoding contract so object encoders receive current result identity directly instead of reading mutable IDs off typed self structs
* Assess changeset merge decision to always use git path (removed `conflicts.IsEmpty()` no-git fast path), with specific focus on performance impact
   * Compare runtime/cost of old no-git path vs current always-git path in no-conflict workloads
   * Confirm whether correctness/cohesion benefits outweigh any measured regression and document outcome
* Sweep remaining raw `srv.Select(..., &ptr, ...)` / `.Self()` ownership boundaries after the current result-detach fixes
   * Confirm there are no more places where a releasable `*Directory` / `*File` / `*Container` pointer is being re-exposed after loading from dagql
   * In particular, keep checking for any helper that unwraps a selected result to a raw pointer and then returns or re-wraps that same pointer
* Remove internal `__immutableRef` schema API once and for all
   * Replace remaining stable-ID use cases with a cleaner non-internal API pattern in dagql/core
* Review the new HTTP implementation for clarity/cohesion
   * Current implementation is functional but confusing; do a low-priority cleanup pass
* Fix `query.__schemaJSONFile` implementation to avoid embedding megabytes of file contents in query args
   * Build/write via ref/snapshot path directly instead of passing huge inline string payloads through select args
* Clean up `cloneContainerForTerminal` usage
   * Find a cleaner container-child pattern for terminal/service callsites instead of special clone helper
* replacing CurrentOpOpts CauseCtx with trace.SpanContextFromContext seems sus, needs checking
* Reassess file mutator parent-passing + lazy-init shape (`WithName`/`WithTimestamps`/`Chown`/`WithReplaced`)
   * Current implementation passes parent object results through schema into core and appears correct in tests, but may not be the most cohesive long-term model.
   * Follow-up: revisit whether lazy-init/parent snapshot modeling can eliminate this explicit parent threading while preserving correctness for service-backed files.
* Assess whether we dropped any git lazyness (especially tree) and whether we should restore it
* Assess whether we really want persistent cache for every schema json file, that's probably a lot of files that are actually kinda sizable!

## Notes
* **THE DAGQL CACHE IS A SINGLETON CREATED ONCE AT ENGINE START AND IT LIVES FOR THE ENTIRE LIFETIME OF THE ENGINE.**
  * There is not a second DAGQL cache.
  * There is not a per-session DAGQL cache.
  * Result-call planning/runtime code should not be written as if cache identity were ambiguous.
  * If a code path needs the DAGQL cache, it should explicitly use or fetch the singleton cache rather than storing mutable cache backpointers on frame/helper structs.
* **CONTAINERD IS TRUSTED. IT IS IN OUR FULL CONTROL. IN THIS USE CASE IT IS NOT SHARED WITH OR DRIVEN BY ANY OTHER SYSTEM.**
  * We should be entirely willing to use containerd directly where it is the right substrate.
  * We should not duplicate state in dagql that containerd already stores correctly and authoritatively for us.
  * Dagql persistence should store only the Dagger-specific state that containerd does not already represent.
* For persistence, it's basically like an export. Don't try to store in-engine numeric ids or something, it's the whole DAG persisted in single-engine-agnostic manner. When loading from persisted disk, you are importing it (including e-graph union and stuff)
  * But also for now, let's be biased towards keeping everything in memory rather than trying to do fancy page out to disk

* **CRITICAL CACHE MODEL RULE: OVERLAPPING DIGESTS MEAN EQUALITY AND FULL INTERCHANGEABILITY.**
  * If two values share any digest / end up in the same digest-equivalence set, that is not merely "evidence" or "similarity"; it means they are the same value for dagql cache purposes and may be reused interchangeably.

* **CRITICAL OWNERSHIP RULE: NEVER RE-EXPOSE A RAW DAGQL-LOADED POINTER FOR A RELEASEABLE OBJECT.**
  * This is a crucial design constraint for the hard-cutover cache/snapshot model.
  * If a helper loads or selects a `Directory`, `File`, or `Container` from dagql, then returning the raw `*Directory` / `*File` / `*Container` pointer is dangerous unless that object is explicitly detached first.
  * The reason is concrete and non-negotiable: these objects own snapshot refs via `OnRelease`, so pointer aliasing can poison some other owner when one result is released.
  * Avoiding this entire class of bugs is crucial.
  * The preferred fix is to preserve `dagql.ObjectResult[*T]` all the way through whenever possible.
  * If a raw `*T` really must be returned, it must be a detached object with reopened/refreshed ownership of any releaseable snapshot state.
  * Internal `Value` shells embedded in larger objects are not public results and must not be handed back out directly as if they were.

* A lot of eval'ing of lazy stuff is just triggered inline now; would be nice if dagql cache scheduler knew about these and could do that in parallel for ya
   * This is partially a pre-existing condition though, so not a big deal yet. But will probably make a great optimization in the near-ish future

# Dockerfile Rebase

## Initial Scope Work

Command used to inspect the requested range while filtering out upstream merge noise:

```bash
git -C /home/sipsma/repo/github.com/sipsma/dagger/wts/dagger3 \
  log --first-parent --cherry-pick --right-only --oneline \
  main...05ab2db9aaa2c018fe7ef5fee08ee773b6c48d77 \
  --ancestry-path 3e7f151a5b840842bfc99dbd988d42502d7bf43b^..05ab2db9aaa2c018fe7ef5fee08ee773b6c48d77
```

I also used the corresponding net diff over the exact inclusive range to inspect the actual file changes:

```bash
git -C /home/sipsma/repo/github.com/sipsma/dagger/wts/dagger3 \
  diff --stat \
  3e7f151a5b840842bfc99dbd988d42502d7bf43b^ \
  05ab2db9aaa2c018fe7ef5fee08ee773b6c48d77 \
  -- \
  core/container.go \
  core/directory.go \
  core/host.go \
  core/integration/dockerfile_test.go \
  core/integration/llbtodagger_test.go \
  core/schema/container.go \
  core/schema/directory.go \
  util/llbtodagger \
  engine/filesync
```

Most important framing:

- This range does not contain the original introduction of the new Dockerfile / `llbtodagger` subsystem.
- That earlier big work is before `3e7f151a5`.
- So this range is mostly follow-up refinement, parity fixes, test additions, cleanup/simplification, and some unrelated branch noise.
- That is the key correction versus the earlier broader analysis.

What actually changed in this range:

### 1. Follow-up copy semantics and path-shape fixes

This is the biggest real production change in the range.

Files involved:

- `util/llbtodagger/convert.go`
- `util/llbtodagger/file.go`
- `util/llbtodagger/exec.go`
- `util/llbtodagger/source.go`
- `core/directory.go`
- `core/schema/directory.go`
- `engine/filesync/filesyncer.go`
- `engine/filesync/localfs.go`
- `engine/filesync/remotefs.go`

Concrete semantic themes from the commits in this range:

- `5d287f449` explicitly fixes `createDestPath` handling.
- `4dff1967d` explicitly fixes local `ADD` auto-unpack behavior.
- `5927d0736` changes `cleanPath` so it preserves a trailing slash.
- `630c741ad` removes `AllowDirectorySourceFallback`.
- `1bdcbec84` removes `destPathHintIsDirectory`.

Practical meaning:

- This range is tightening and simplifying ambiguous Dockerfile `COPY` / `ADD` behavior.
- Especially around destination-path interpretation and archive unpacking.
- It is backing away from some heuristics that had been introduced earlier.
- So the branch is not adding a brand-new capability here. It is mostly deciding which hidden compatibility knobs were actually wrong or unnecessary, and how strict the copy semantics should be.

### 2. Dockerfile parity tests were expanded

This range adds real integration coverage around behavior that already existed or was being refined.

Main file:

- `core/integration/dockerfile_test.go`

The branch-added subtests in this range, compared to the earlier state, are:

- `healthcheck`
- `missing secret fails when required is set`
- `missing secret is ok when required is false`
- the case that `ADD` of an HTTP URL should not unpack automatically

That lines up with these commits:

- `795db6d39` test for healthcheck
- `e94f70cae` add test for missing secret
- `aee7a93de` test for non-required missing secret
- `9fc6b4a3c` test for the case that an `ADD` of a http method doesnt unpack

This is probably the cleanest high-signal payload in the range:

- these are real parity expectations
- they are not just churn
- and they tell us what branch-side behavior the author was trying to lock in

### 3. `container.build(...)` / schema-core Dockerfile wiring was touched

Files involved:

- `core/container.go`
- `core/schema/container.go`
- `core/host.go`

The commit messages in this range do not isolate this cleanly because a lot of it came in under `wip` / `fix` / `cleanup` commits, but by the end of the range the branch state clearly includes:

- public `container.build(...)` re-enabled in schema
- continued wiring of Dockerfile build through the hard-cutover path
- related support code in `core/container.go` / `core/host.go`

So this range is not where the hard-cutover Dockerfile path was born, but it does include branch-side work to make that path actually usable from schema-facing APIs and parity tests.

### 4. A local copy of `directory.chown` work also landed in the range

Files involved:

- `core/directory.go`
- `core/schema/directory.go`

Commits:

- `b4f3c06f9` feat: directory.chown supports username/groupname
- `bf178a5f0` remove dir permission (dont think its actually needed)

This is a little messy because the same feature later exists upstream too, but within the requested range it is part of the branch’s local evolution, and it intersects directly with Dockerfile `COPY --chown` / ownership semantics.

So it is part of the production surface touched by this range, even though it is not exclusively “new Dockerfile implementation” work.

### 5. There is unrelated branch noise in the range

The clearest unrelated item is:

- `c8fc29c8c` feat: go toolchain tag support

And then there is a lot of:

- `wip`
- `cleanup`
- `lint cleanup`
- `removed some unneeded lines for linting`
- `regenerated`
- `update golden tests`

So the history in this exact range is not a clean series of intention-revealing commits.
The real semantic payload is much smaller than the commit count makes it look.

Net relevant files touched in this range:

If I limit to the Dockerfile / `llbtodagger` area and ignore the broader repo churn, the range touches:

- `core/container.go`
- `core/directory.go`
- `core/host.go`
- `core/integration/dockerfile_test.go`
- `core/integration/llbtodagger_test.go`
- `core/schema/container.go`
- `core/schema/directory.go`
- `engine/filesync/filesyncer.go`
- `engine/filesync/localfs.go`
- `engine/filesync/remotefs.go`
- `util/llbtodagger/convert.go`
- `util/llbtodagger/exec.go`
- `util/llbtodagger/file.go`
- `util/llbtodagger/metadata.go`
- `util/llbtodagger/source.go`

Corrected takeaway:

- The earlier broad look overestimated how much “new subsystem” was in play here.
- For this exact range, the branch is mostly doing:
  - refinement of already-existing `llbtodagger` copy / add semantics
  - rethinking some copy heuristics
  - adding Dockerfile parity tests for healthcheck, missing secret required vs optional, and HTTP `ADD` should not auto-unpack
  - plus some schema/core wiring to keep the hard-cutover Dockerfile path usable
  - plus unrelated noise like go toolchain tag support
- The high-signal items from this range are:
  - the parity tests
  - the copy-path semantic decisions
  - the trailing-slash / `createDestPath` / unpack fixes
  - and the schema re-exposure of `container.build(...)`
- The low-signal items are:
  - the merge commits themselves
  - generated churn
  - lint-only cleanup
  - unrelated toolchain work

## Implementation Plan

This implementation pass should only change files that close the still-missing, high-signal gaps from the requested range on top of our current branch.

The key correction from the earlier draft is this:

- For Dockerfile-specific features, hidden knobs, and compatibility behavior, the final state of the requested branch range is the source of truth.
- That means we do not keep our current `requiredSourcePath` / `destPathHintIsDirectory` / `copySourcePathContentsWhenDir` model just because it exists on our branch.
- Instead, we follow the branch’s final split:
  - generic container-level `withDirectory` / `withFile` stay generic
  - Dockerfile directory-copy fidelity moves to a dedicated internal `__withDirectoryDockerfileCompat(...)` path
  - lower-level directory `withFile(...)` keeps the branch-final hidden file-copy knobs that were not superseded in this range (`doNotCreateDestPath`, `attemptUnpackDockerCompatibility`)
  - `cleanPath` preserves trailing slashes so path shape itself carries part of the directory-destination intent

We are still adapting that branch model onto our current architecture. So we are not porting older unrelated schema/core/filesync scaffolding wholesale. But for Dockerfile-specific behavior, the branch range leads.

In particular:

- We **will** restore the deprecated public `container.build(...)` wrapper on top of our current hard-cutover `Container.Build(...)`.
- We **will** import the Dockerfile parity coverage added in the branch range.
- We **will** move Dockerfile directory-copy semantics out of generic `withDirectory` and into a dedicated internal `__withDirectoryDockerfileCompat(...)` path.
- We **will** keep the branch-final lower-level `withFile(...)` hidden args that still exist in the branch range for file-copy fidelity, while removing the fallback hacks and the branch-superseded directory-copy knobs.
- We **will** remove the current branch-only Dockerfile knobs that the range superseded:
  - `AllowDirectorySourceFallback`
  - `RequiredSourcePath`
  - `DestPathHintIsDirectory`
  - `CopySourcePathContentsWhenDir`
- We **will** preserve destination trailing slash in `cleanPath(...)` because the branch range relies on path shape instead of those removed knobs.
- We **will not** port the branch’s older DagOp/buildkit schema rewrites in unrelated files.

### core/schema/container.go

Restore the deprecated `container.build(...)` entrypoint and strip generic container copy plumbing back to the generic behavior expected by the final branch state.

1. Re-enable the public schema field in `Install` instead of leaving the old TODO block commented out.

```go
dagql.NodeFunc("build", s.build).
	View(BeforeVersion("v0.19.0")).
	Deprecated("Use `Directory.build` instead").
	Doc(`Initializes this container from a Dockerfile build.`).
	Args(
		dagql.Arg("context").Doc("Directory context used by the Dockerfile."),
		dagql.Arg("dockerfile").Doc("Path to the Dockerfile to use."),
		dagql.Arg("target").Doc("Target build stage to build."),
		dagql.Arg("buildArgs").Doc("Additional build arguments."),
		dagql.Arg("secrets").Doc(`Secrets to pass to the build.`),
		dagql.Arg("noInit").Doc(`If set, skip the automatic init process injected into containers created by RUN statements.`),
	),
```

2. Add a concrete `containerBuildArgs` type local to this file.

```go
type containerBuildArgs struct {
	Context    core.DirectoryID
	Dockerfile string                             `default:"Dockerfile"`
	Target     string                             `default:""`
	BuildArgs  []dagql.InputObject[core.BuildArg] `default:"[]"`
	Secrets    []core.SecretID                    `default:"[]"`
	NoInit     bool                               `default:"false"`
}
```

Deliberate design choice: do **not** add `ssh` here. This field is restored under `View(BeforeVersion("v0.19.0"))`, so we should preserve the historical legacy API shape instead of retrofitting newer `Directory.dockerBuild(...)` arguments onto an old-version compatibility surface.

3. Add the `build` resolver as a thin wrapper over the existing hard-cutover build path, mirroring `directorySchema.dockerBuild(...)` as closely as possible.

```go
func (s *containerSchema) build(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Container],
	args containerBuildArgs,
) (*core.Container, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	srv, err := query.Server.Server(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get server: %w", err)
	}

	contextDir, err := args.Context.Load(ctx, srv)
	if err != nil {
		return nil, err
	}
	buildctxDir, err := applyDockerIgnore(ctx, srv, contextDir, args.Dockerfile)
	if err != nil {
		return nil, err
	}

	secrets, err := dagql.LoadIDResults(ctx, srv, args.Secrets)
	if err != nil {
		return nil, err
	}

	buildctxDirID, err := buildctxDir.RecipeID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get build context recipe ID: %w", err)
	}

	return parent.Self().Build(
		ctx,
		contextDir.Self(),
		buildctxDirID,
		args.Dockerfile,
		collectInputsSlice(args.BuildArgs),
		args.Target,
		secrets,
		args.NoInit,
		dagql.ObjectResult[*core.Socket]{},
	)
}
```

4. Simplify `containerWithDirectoryArgs` and `containerWithFileArgs` to match the branch’s final container-facing shape. In particular:

- `withDirectory(...)` should stop passing the Dockerfile-only directory-copy knobs into `core.Container.WithDirectory(...)`.
- `withFile(...)` should stop threading Dockerfile-only copy knobs into `core.Container.WithFile(...)`.
- the lower-level directory-schema `withFile(...)` path still keeps the branch-final hidden file-copy args; this point is specifically about the container-facing wrappers matching the branch’s final split.

5. Remove the current file-source fallback branch from `withFile(...)` entirely. The final branch state does not keep `AllowDirectorySourceFallback`, and once `llbtodagger` emits `__withDirectoryDockerfileCompat(...)` for Dockerfile copy actions, this reconstruction hack should disappear with it.

The resulting load path should be just:

```go
file, err := args.Source.Load(ctx, srv)
if err != nil {
	return inst, err
}
```

6. No generated SDK/docs work is planned from this file restoration.

Because the restored field remains gated by:

```go
View(BeforeVersion("v0.19.0"))
```

it should not affect current-version codegen outputs. We should still confirm that empirically after implementation, but we should not plan to hand-edit or regenerate SDK/docs files as part of this pass unless the actual generation diff proves otherwise.

### core/schema/directory.go

Split generic directory copy from Dockerfile-specific directory copy, following the final branch model.

1. Simplify the generic `WithDirectoryArgs` back down to a generic copy API with no Dockerfile-only hidden args.

Target:

```go
type WithDirectoryArgs struct {
	Path  string
	Owner string `default:""`

	Source    core.DirectoryID
	Directory core.DirectoryID // legacy, use Source instead

	core.CopyFilter
}
```

2. Add a dedicated Dockerfile-only internal args type and internal schema field, matching the final branch surface.

```go
type WithDirectoryDockerfileCompatArgs struct {
	Path        string
	Owner       string `default:""`
	Permissions dagql.Optional[dagql.Int]

	SrcPath                          string `internal:"true" default:""`
	FollowSymlink                    bool   `internal:"true" default:"false"`
	DirCopyContents                  bool   `internal:"true" default:"false"`
	AttemptUnpackDockerCompatibility bool   `internal:"true" default:"false"`
	CreateDestPath                   bool   `internal:"true" default:"false"`
	AllowWildcard                    bool   `internal:"true" default:"false"`
	AllowEmptyWildcard               bool   `internal:"true" default:"false"`
	AlwaysReplaceExistingDestPaths   bool   `internal:"true" default:"false"`

	Source core.DirectoryID
	core.CopyFilter
}
```

Install it as a new internal field:

```go
dagql.NodeFunc("__withDirectoryDockerfileCompat", s.withDirectoryDockerfileCompat).
	Doc(`(Internal-only) Dockerfile-compat directory copy path.`).
	Args(
		dagql.Arg("path"),
		dagql.Arg("source"),
		dagql.Arg("owner"),
		dagql.Arg("permissions"),
		dagql.Arg("include"),
		dagql.Arg("exclude"),
		dagql.Arg("gitignore"),
	)
```

The hidden Dockerfile-compat knobs stay on the args struct itself. They are intentionally **not** added to the public `.Args(...)` list; the converter is the only caller and it should keep using the hidden/internal surface.

3. Implement the new resolver in our current lazy style: load the source ID, create a child directory, and install a Dockerfile-compat lazy payload on it. Do **not** eagerly execute the copy in schema.

```go
func (s *directorySchema) withDirectoryDockerfileCompat(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Directory],
	args WithDirectoryDockerfileCompatArgs,
) (dagql.ObjectResult[*core.Directory], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Directory]{}, err
	}
	srcDir, err := args.Source.Load(ctx, srv)
	if err != nil {
		return dagql.ObjectResult[*core.Directory]{}, err
	}

	var perms *int
	if args.Permissions.Valid {
		p := int(args.Permissions.Value)
		perms = &p
	}

	dir := core.NewDirectoryChild(parent)
	dir.Lazy = &core.DirectoryWithDirectoryDockerfileCompatLazy{
		LazyState:                        core.NewLazyState(),
		Parent:                           parent,
		DestDir:                          args.Path,
		SrcPath:                          args.SrcPath,
		Source:                           srcDir,
		Filter:                           args.CopyFilter,
		Owner:                            args.Owner,
		Permissions:                      perms,
		FollowSymlink:                    args.FollowSymlink,
		DirCopyContents:                  args.DirCopyContents,
		AttemptUnpackDockerCompatibility: args.AttemptUnpackDockerCompatibility,
		CreateDestPath:                   args.CreateDestPath,
		AllowWildcard:                    args.AllowWildcard,
		AllowEmptyWildcard:               args.AllowEmptyWildcard,
		AlwaysReplaceExistingDestPaths:   args.AlwaysReplaceExistingDestPaths,
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
}
```

4. Simplify `WithFileArgs` by removing `AllowDirectorySourceFallback`, and remove the current fallback branch from `withFile(...)`.

Target shape:

```go
type WithFileArgs struct {
	Path        string
	Source      core.FileID
	Permissions dagql.Optional[dagql.Int]
	Owner       string `default:""`
	DoNotCreateDestPath bool `internal:"true" default:"false"`
	AttemptUnpackDockerCompatibility bool `internal:"true" default:"false"`
}
```

### core/container.go

Strip the generic container copy paths back to generic semantics, again following the final branch model.

1. Simplify `Container.WithDirectory(...)` so it no longer carries Dockerfile-only knobs.

Target:

```go
func (container *Container) WithDirectory(
	ctx context.Context,
	parent dagql.ObjectResult[*Container],
	subdir string,
	src dagql.ObjectResult[*Directory],
	filter CopyFilter,
	owner string,
) (*Container, error)
```

This method should keep our current ownership-aware structure, but the only semantics it should preserve are:

- locate mount path
- resolve owner names if needed
- select generic `withDirectory`
- update rootfs / mounted-directory cases

2. Simplify `Container.WithFile(...)` the same way. Following the final branch state, it should no longer carry Dockerfile-only flags like `DoNotCreateDestPath` or `AttemptUnpackDockerCompatibility`.

Target:

```go
func (container *Container) WithFile(
	ctx context.Context,
	parent dagql.ObjectResult[*Container],
	srv *dagql.Server,
	destPath string,
	src dagql.ObjectResult[*File],
	permissions *int,
	owner string,
) (*Container, error)
```

3. Remove Dockerfile-only fields from the container lazy/persisted structs:

- `ContainerWithDirectoryLazy`
- `persistedContainerWithDirectoryLazy`
- `ContainerWithFileLazy`
- `persistedContainerWithFileLazy`

Specifically remove:

- `DoNotCreateDestPath`
- `AttemptUnpackDockerCompatibility`
- `RequiredSourcePath`
- `DestPathHintIsDirectory`
- `CopySourcePathContentsWhenDir`

from the generic container-copy path. This is specifically about the container-owned lazy structs; it does **not** imply that the lower-level directory `withFile(...)` path drops the branch-final hidden args that still exist there.

4. Update all callsites in `core/schema/container.go` and internal lazy evaluation so the generic path only uses the simplified generic signatures above.

### core/directory.go

Mirror the final branch split in core:

- generic `Directory.WithDirectory(...)` becomes generic again
- a new `Directory.WithDirectoryDockerfileCompat(...)` owns Dockerfile-only copy semantics

1. Simplify `DirectoryWithDirectoryLazy` and `persistedDirectoryWithDirectoryLazy` to the generic form.

Target:

```go
type DirectoryWithDirectoryLazy struct {
	LazyState
	Parent dagql.ObjectResult[*Directory]
	DestDir string
	Source dagql.ObjectResult[*Directory]
	Filter CopyFilter
	Owner string
}
```

with a matching persisted JSON struct carrying only:

```go
ParentResultID uint64
DestDir string
SourceResultID uint64
Filter CopyFilter
Owner string
```

2. Add a new Dockerfile-specific lazy/persisted pair.

```go
type DirectoryWithDirectoryDockerfileCompatLazy struct {
	LazyState
	Parent dagql.ObjectResult[*Directory]
	DestDir string
	SrcPath string
	Source dagql.ObjectResult[*Directory]
	Filter CopyFilter
	Owner string
	Permissions *int
	FollowSymlink bool
	DirCopyContents bool
	AttemptUnpackDockerCompatibility bool
	CreateDestPath bool
	AllowWildcard bool
	AllowEmptyWildcard bool
	AlwaysReplaceExistingDestPaths bool
}
```

with a matching persisted JSON struct carrying the same fields.

3. Wire the new lazy type into the same persistence/evaluation machinery as the rest of our directory lazy model.

That means adding:

- a `decodePersistedDirectoryLazy(...)` case for the new persisted compat payload
- `Evaluate(...)`, `AttachDependencies(...)`, and `EncodePersisted(...)` methods on `DirectoryWithDirectoryDockerfileCompatLazy`

Target shape:

```go
func (lazy *DirectoryWithDirectoryDockerfileCompatLazy) Evaluate(ctx context.Context, dir *Directory) error {
	return lazy.LazyState.Evaluate(ctx, "Directory.withDirectoryDockerfileCompat", func(ctx context.Context) error {
		return dir.WithDirectoryDockerfileCompat(
			ctx,
			lazy.Parent,
			lazy.DestDir,
			lazy.SrcPath,
			lazy.Source,
			lazy.Filter,
			lazy.Owner,
			lazy.Permissions,
			lazy.FollowSymlink,
			lazy.DirCopyContents,
			lazy.AttemptUnpackDockerCompatibility,
			lazy.CreateDestPath,
			lazy.AllowWildcard,
			lazy.AllowEmptyWildcard,
			lazy.AlwaysReplaceExistingDestPaths,
		)
	})
}
```

4. Add a new core method matching the branch’s Dockerfile-specific compat surface, but expressed in our current lazy/mutating `Directory` model:

```go
func (dir *Directory) WithDirectoryDockerfileCompat(
	ctx context.Context,
	parent dagql.ObjectResult[*Directory],
	destDir string,
	srcPath string,
	src dagql.ObjectResult[*Directory],
	filter CopyFilter,
	owner string,
	permissions *int,
	followSymlink bool,
	dirCopyContents bool,
	attemptUnpackDockerCompatibility bool,
	createDestPath bool,
	allowWildcard bool,
	allowEmptyWildcard bool,
	alwaysReplaceExistingDestPaths bool,
) error
```

This is where we adapt the branch’s compat API shape onto our branch’s lazy architecture. The external compat knobs still match what the converter emits, but the implementation follows our existing `Directory.WithDirectory(...)` mutation/evaluation pattern rather than the branch’s eager clone-returning shape.

5. The important body should follow the branch’s final semantics, not our current generic hidden-arg approach:

```go
dagqlCache, err := dagql.EngineCache(ctx)
if err != nil {
	return err
}
if err := dagqlCache.Evaluate(ctx, parent, src); err != nil {
	return err
}

dirRef, err := parent.Self().getSnapshot()
if err != nil {
	return fmt.Errorf("failed to get directory ref: %w", err)
}
srcRef, err := src.Self().getSnapshot()
if err != nil {
	return fmt.Errorf("failed to get source directory ref: %w", err)
}

destDir = path.Join(dir.Dir, destDir)

newRef, err := query.SnapshotManager().New(
	ctx,
	dirRef,
	bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
	bkcache.WithDescription("Directory.withDirectoryDockerfileCompat"),
)
if err != nil {
	return fmt.Errorf("snapshotmanager.New failed: %w", err)
}
defer newRef.Release(context.WithoutCancel(ctx))

err = MountRef(ctx, newRef, func(copyDest string, destMnt *mount.Mount) error {
	resolvedCopyDest, err := containerdfs.RootPath(copyDest, destDir)
	if err != nil {
		return err
	}
	if srcRef == nil {
		return os.MkdirAll(resolvedCopyDest, 0o755)
	}

	// mount source, build fscopy opts, and preserve the branch-final
	// Dockerfile compat flow around srcPath / trailing slash / createDestPath

	if attemptUnpackDockerCompatibility {
		destPathHintIsDirectory := strings.HasSuffix(resolvedCopyDest, "/")
		didUnpack, err := attemptCopyArchiveUnpack(
			ctx,
			mountedSrcPath,
			srcPathCopy,
			resolvedCopyDest,
			filter.Include,
			filter.Exclude,
			filter.Gitignore,
			ownership,
			permissions,
			newRef.IdentityMapping(),
			destPathHintIsDirectory,
		)
		if err != nil {
			return fmt.Errorf("failed to unpack source archive: %w", err)
		}
		if didUnpack {
			return nil
		}
	}

	pathsToCopy := []string{src.Self().Dir}
	if srcPath != "" {
		if src.Self().Dir != "" && src.Self().Dir != "/" {
			srcPath = src.Self().Dir + "/" + srcPath
		}
		pathsToCopy, err = fscopy.ResolveWildcards(mountedSrcPath, srcPath, true)
		if err != nil {
			return err
		}
	}

	for _, srcPath := range pathsToCopy {
		copyDestPath := destDir
		if strings.HasSuffix(destDir, "/") && !strings.HasSuffix(copyDestPath, "/") {
			copyDestPath += "/"
		}
		if !createDestPath {
			destDirPath := filepath.Dir(path.Join(copyDest, copyDestPath))
			if _, err := os.Lstat(destDirPath); err != nil {
				return TrimErrPathPrefix(err, path.Join(mountedSrcPath, src.Self().Dir))
			}
		}
		if err := fscopy.Copy(ctx, mountedSrcPath, srcPath, copyDest, copyDestPath, opts...); err != nil {
			return fmt.Errorf("failed to copy source directory: %w", err)
		}
	}
	return nil
})
if err != nil {
	return err
}

ref, err := newRef.Commit(ctx)
if err != nil {
	return fmt.Errorf("failed to commit copied directory: %w", err)
}
return dir.setSnapshot(ref)
```

This is the big semantic pivot:

- no `requiredSourcePath`
- no `copySourcePathContentsWhenDir`
- no persisted `destPathHintIsDirectory`
- trailing slash on `path` plus explicit `srcPath` / `dirCopyContents` / `createDestPath` drive the Dockerfile compatibility behavior instead

The compat signature intentionally still carries:

- `followSymlink`
- `dirCopyContents`
- `allowWildcard`
- `allowEmptyWildcard`
- `alwaysReplaceExistingDestPaths`

because that is the Dockerfile-converter-facing compat surface the branch emits. We do **not** invent extra compat knobs beyond that set, and we do **not** keep the superseded knobs that the branch stopped emitting.

6. Keep the generic `Directory.WithDirectory(...)` implementation simple and generic, matching the branch’s final model: just merge/copy a directory ID into a destination path with filter + owner.

7. Do **not** remove the branch-final hidden file-copy knobs from `Directory.WithFile(...)` or `WithFileArgs` in this pass. The requested range removes the directory-copy reconstruction knobs and fallback hacks, but it does not hard-cut `doNotCreateDestPath` / `attemptUnpackDockerCompatibility` out of the lower-level file path.

### util/llbtodagger/convert.go

Preserve destination trailing slash in `cleanPath(...)` because the final branch uses path shape itself as part of Dockerfile copy intent.

Target:

```go
func cleanPath(p string) string {
	if p == "" {
		return "/"
	}
	trailingSlash := strings.HasSuffix(p, "/") || strings.HasSuffix(p, "/.")
	p = path.Clean(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if trailingSlash && !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}
```

This stays in the plan exactly because the branch range explicitly introduced it, and for Dockerfile-specific path-shape semantics the branch range leads.

### util/llbtodagger/file.go

Adopt the branch’s final Dockerfile-only lowering model: `COPY` / `ADD` file actions should lower to `__withDirectoryDockerfileCompat(...)`, not to the current generic `withDirectory(...)` / `withFile(...)` hidden-arg combinations.

1. Replace the current split lowering logic in `applyCopy(...)` with the branch-style compat call.

Target shape:

```go
args := []*call.Argument{
	argString("srcPath", cleanPath(cp.Src)),
	argString("path", cleanPath(cp.Dest)),
	argID("source", sourceID),
}

if cp.FollowSymlink {
	args = append(args, argBool("followSymlink", true))
}
if cp.DirCopyContents {
	args = append(args, argBool("dirCopyContents", true))
}
if cp.AttemptUnpackDockerCompatibility {
	args = append(args, argBool("attemptUnpackDockerCompatibility", true))
}
if cp.CreateDestPath {
	args = append(args, argBool("createDestPath", true))
}
if cp.AllowWildcard {
	args = append(args, argBool("allowWildcard", true))
}
if cp.AllowEmptyWildcard {
	args = append(args, argBool("allowEmptyWildcard", true))
}
if len(cp.IncludePatterns) > 0 {
	args = append(args, argStringList("include", cp.IncludePatterns))
}
if len(cp.ExcludePatterns) > 0 {
	args = append(args, argStringList("exclude", cp.ExcludePatterns))
}
if owner != "" {
	args = append(args, argString("owner", owner))
}
if cp.Mode >= 0 {
	args = append(args, argInt("permissions", int64(cp.Mode)))
}

return appendCall(baseID, directoryType(), "__withDirectoryDockerfileCompat", args...), baseContainerID, nil
```

2. Remove the now-obsolete generic-path helpers from this file:

- `explicitFileCopyPath`
- `requiredSourcePathForCopy`
- `copySourcePathContentsWhenDir`
- `copyDestPathHintIsDirectory`

Those existed only to make the generic path behave like Dockerfile copy semantics. The final branch model is to stop doing that and route Dockerfile copy semantics through the dedicated internal API instead.

3. Keep the final branch behavior that `AlwaysReplaceExistingDestPaths` is still rejected at conversion time.

```go
if cp.AlwaysReplaceExistingDestPaths {
	return nil, nil, fmt.Errorf("alwaysReplaceExistingDestPaths is unsupported")
}
```

4. When porting the branch code, normalize the emitted GraphQL arg name to `allowEmptyWildcard` in lower camel case. The branch snapshot currently shows `AllowEmptyWildcard` in one place, but the schema field is lower camel and the compat surface should use the schema-consistent spelling.

### util/llbtodagger/convert_test.go

Delete this file.

The requested branch range deletes the old util-level converter tests rather than carrying them forward. They are anchored to the superseded generic hidden-arg lowering model, and retaining them while we switch to `__withDirectoryDockerfileCompat(...)` would be the wrong source of truth.

### util/llbtodagger/dockerfile_convert_test.go

Delete this file.

Same reason as above: the branch we are following does not carry these util-level Dockerfile converter tests forward. Coverage responsibility lives in the integration-level llbtodagger and Dockerfile suites instead of preserving these stale direct-shape tests.

### core/integration/dockerfile_test.go

Import the Dockerfile parity coverage that was added in the branch range and is still missing here.

1. Add the two missing-secret cases into `TestDockerBuild`, immediately after the existing `"with build secrets"` coverage.

Required-missing-secret failure:

```go
t.Run("missing secret fails when required is set", func(ctx context.Context, t *testctx.T) {
	dockerfile := `FROM ` + alpineImage + `
RUN --mount=type=secret,id=my-secret,required=true echo this should not run
`
	dir := baseDir.WithNewFile("Dockerfile", dockerfile)

	_, err := dir.DockerBuild().Sync(ctx)
	require.Error(t, err)
	require.ErrorContains(t, err, `secret "my-secret" is required but no secret mappings were provided`)
})
```

Optional-missing-secret success:

```go
t.Run("missing secret is ok when required is false", func(ctx context.Context, t *testctx.T) {
	dockerfile := `FROM ` + alpineImage + `
RUN --mount=type=secret,id=my-secret,required=false echo this is fine
`
	dir := baseDir.WithNewFile("Dockerfile", dockerfile)

	_, err := dir.DockerBuild().Sync(ctx)
	require.NoError(t, err)
})
```

2. Add the missing healthcheck coverage into `TestDockerBuild`, near the other metadata/parity assertions.

```go
t.Run("healthcheck", func(ctx context.Context, t *testctx.T) {
	dockerfile := `FROM ` + alpineImage + `
HEALTHCHECK --interval=21s --timeout=4s --start-period=9s --start-interval=2s --retries=5 CMD ["sh","-c","test -d /"]
`
	dir := baseDir.WithNewFile("Dockerfile", dockerfile)

	healthcheck := dir.DockerBuild().DockerHealthcheck()

	interval, err := healthcheck.Interval(ctx)
	require.NoError(t, err)
	require.Equal(t, "21s", interval)

	timeout, err := healthcheck.Timeout(ctx)
	require.NoError(t, err)
	require.Equal(t, "4s", timeout)

	startPeriod, err := healthcheck.StartPeriod(ctx)
	require.NoError(t, err)
	require.Equal(t, "9s", startPeriod)

	startInterval, err := healthcheck.StartInterval(ctx)
	require.NoError(t, err)
	require.Equal(t, "2s", startInterval)

	retries, err := healthcheck.Retries(ctx)
	require.NoError(t, err)
	require.Equal(t, 5, retries)

	args, err := healthcheck.Args(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"sh", "-c", "test -d /"}, args)
})
```

3. Add the missing “HTTP `ADD` does not auto-unpack” integration as a standalone test alongside the other `ADD` / unpack coverage.

```go
func (DockerfileSuite) TestAddHTTPDoesNotUnpack(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	svc := c.Container().
		From(alpineImage).
		WithDirectory("/srv", c.Directory().WithNewFile("mydir/data", "hello")).
		WithExec([]string{"sh", "-c", "cd /srv && tar czf remotedir.tar.gz mydir"}).
		WithExposedPort(80).
		WithDefaultArgs([]string{"httpd", "-v", "-f"}).
		AsService()

	dir := c.Directory().WithNewFile("Dockerfile", `
FROM ` + alpineImage + `
ADD http://fileserver/remotedir.tar.gz this-should-not-unpack
`)

	ctr := dir.DockerBuild().
		WithServiceBinding("fileserver", svc)

	_, err := ctr.WithExec([]string{"test", "-f", "this-should-not-unpack"}).Sync(ctx)
	require.NoError(t, err)

	s, err := ctr.WithExec([]string{"sh", "-c", "mkdir the-dir && tar xzf this-should-not-unpack -C the-dir"}).File("the-dir/mydir/data").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello", s)
}
```

4. Keep the existing skipped checksum-on-HTTP tests exactly as-is for now. This pass is about importing the branch-range parity cases we actually care about, not changing the HTTP-checksum support decision.

### dagql/idtui/testdata/TestTelemetry/TestGolden/docker-build

After the Dockerfile copy path is rerouted through `__withDirectoryDockerfileCompat(...)` and the restored `container.build(...)` wrapper is back, rerun the focused telemetry golden tests for docker-build and update this golden if the traced call shape changes.

The key point is not to hand-edit it from memory. The file should be regenerated from the focused telemetry test run after the implementation is done.

### dagql/idtui/testdata/TestTelemetry/TestGolden/docker-build-fail

Same as the success golden above: rerun the focused telemetry test and update this file only if the internal call graph change produces a new expected trace.
