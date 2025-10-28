package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"dagger.io/dagger/telemetry"
	containerdfs "github.com/containerd/continuity/fs"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/buildkit"
	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/client/llb"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/trace"
)

// NewChangeset creates a Changeset object with all fields computed upfront
func NewChangeset(ctx context.Context, before, after dagql.ObjectResult[*Directory]) (*Changeset, error) {
	changes := &Changeset{
		Before: before,
		After:  after,
	}

	// Compute all the changes once
	if err := changes.computeChanges(ctx); err != nil {
		return nil, err
	}

	return changes, nil
}

// computeChanges calculates added, changed, and removed files/paths
func (ch *Changeset) computeChanges(ctx context.Context) error {
	if ch.Before.ID().Digest() == ch.After.ID().Digest() {
		// No changes if the directories are identical
		return nil
	}

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return err
	}

	// Get all paths from before and after directories
	var beforePaths, afterPaths, diffPaths []string
	if err := srv.Select(ctx, ch.Before, &beforePaths, dagql.Selector{
		Field: "glob",
		Args:  []dagql.NamedInput{{Name: "pattern", Value: dagql.String("**")}},
	}); err != nil {
		return fmt.Errorf("failed to get paths from before directory: %w", err)
	}
	if err := srv.Select(ctx, ch.After, &afterPaths, dagql.Selector{
		Field: "glob",
		Args:  []dagql.NamedInput{{Name: "pattern", Value: dagql.String("**")}},
	}); err != nil {
		return fmt.Errorf("failed to get paths from after directory: %w", err)
	}
	// Get diff paths (changed + added files)
	if err := srv.Select(ctx, ch.Before, &diffPaths, dagql.Selector{
		Field: "diff",
		Args: []dagql.NamedInput{
			{Name: "other", Value: dagql.NewID[*Directory](ch.After.ID())},
		},
	}, dagql.Selector{
		Field: "glob",
		Args:  []dagql.NamedInput{{Name: "pattern", Value: dagql.String("**")}},
	}); err != nil {
		return fmt.Errorf("failed to get paths from diff directory: %w", err)
	}

	// Create sets for efficient lookups
	beforePathSet := make(map[string]bool, len(beforePaths))
	for _, path := range beforePaths {
		beforePathSet[path] = true
	}

	afterPathSet := make(map[string]bool, len(afterPaths))
	for _, path := range afterPaths {
		afterPathSet[path] = true
	}

	diffPathSet := make(map[string]bool, len(diffPaths))
	for _, path := range diffPaths {
		diffPathSet[path] = true
	}

	// Compute added files (in after but not in before, and files only)
	for _, path := range afterPaths {
		if !beforePathSet[path] {
			ch.AddedPaths = append(ch.AddedPaths, path)
		}
	}

	// Create set of added files for efficient lookup
	addedFileSet := make(map[string]bool, len(ch.AddedPaths))
	for _, path := range ch.AddedPaths {
		addedFileSet[path] = true
	}

	// Compute changed files (in diff but not added, and files only)
	for _, path := range diffPaths {
		// FIXME: we shouldn't skip if the _only_ thing changed was the directory,
		// i.e. it's not listed here because children were modified, but because the
		// directory itself was chmodded or something
		if !strings.HasSuffix(path, "/") && !addedFileSet[path] {
			ch.ModifiedPaths = append(ch.ModifiedPaths, path)
		}
	}

	// Compute removed paths (in before but not in after)
	var allRemovedPaths []string
	for _, path := range beforePaths {
		if !afterPathSet[path] {
			allRemovedPaths = append(allRemovedPaths, path)
		}
	}

	// Filter out children of removed directories to avoid redundancy
	dirs := make(map[string]bool)

removed:
	for _, fp := range allRemovedPaths {
		// Check if this path is a child of an already removed directory
		for dir := range dirs {
			if strings.HasPrefix(fp, dir) {
				// don't show removed files in directories that were already removed
				continue removed
			}
		}
		// if the path ends with a slash, it's a directory
		if strings.HasSuffix(fp, "/") {
			dirs[fp] = true
		}
		ch.RemovedPaths = append(ch.RemovedPaths, fp)
	}
	ch.allRemovedPaths = allRemovedPaths

	return nil
}

type Changeset struct {
	Before dagql.ObjectResult[*Directory] `field:"true" doc:"The older/lower snapshot to compare against."`
	After  dagql.ObjectResult[*Directory] `field:"true" doc:"The newer/upper snapshot."`

	AddedPaths    []string `field:"true" doc:"Files and directories that were added in the newer directory."`
	ModifiedPaths []string `field:"true" doc:"Files and directories that existed before and were updated in the newer directory."`
	RemovedPaths  []string `field:"true" doc:"Files and directories that were removed. Directories are indicated by a trailing slash, and their child paths are not included."`

	// same as above, but includes all removed paths (children of removed dirs too)
	allRemovedPaths []string
}

func (*Changeset) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Changeset",
		NonNull:   true,
	}
}

func (*Changeset) TypeDescription() string {
	return "A comparison between two directories representing changes that can be applied."
}

var _ Evaluatable = (*Changeset)(nil)

func (ch *Changeset) Evaluate(context.Context) (*buildkit.Result, error) {
	return nil, nil
}

var _ HasPBDefinitions = (*Changeset)(nil)

func (ch *Changeset) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	beforeDefs, err := ch.Before.Self().PBDefinitions(ctx)
	if err != nil {
		return nil, err
	}
	afterDefs, err := ch.After.Self().PBDefinitions(ctx)
	if err != nil {
		return nil, err
	}
	return append(beforeDefs, afterDefs...), nil
}

const ChangesetPatchFilename = "diff.patch"

func (ch *Changeset) AsPatch(ctx context.Context) (*File, error) {
	beforeRef, err := getRefOrEvaluate(ctx, ch.Before.Self())
	if err != nil {
		return nil, err
	}

	afterRef, err := getRefOrEvaluate(ctx, ch.After.Self())
	if err != nil {
		return nil, err
	}

	bkSessionGroup, ok := buildkit.CurrentBuildkitSessionGroup(ctx)
	if !ok {
		return nil, fmt.Errorf("no buildkit session group in context")
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}

	opt, ok := buildkit.CurrentOpOpts(ctx)
	if !ok {
		return nil, fmt.Errorf("no buildkit opts in context")
	}
	ctx = trace.ContextWithSpanContext(ctx, opt.CauseCtx)
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	defer stdio.Close()

	newRef, err := query.BuildkitCache().New(ctx, nil, bkSessionGroup,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("Changeset.asPatch"))
	if err != nil {
		return nil, err
	}
	err = MountRef(ctx, beforeRef, bkSessionGroup, func(before string) error {
		beforeDir, err := containerdfs.RootPath(before, ch.Before.Self().Dir)
		if err != nil {
			return err
		}
		return MountRef(ctx, afterRef, bkSessionGroup, func(after string) error {
			afterDir, err := containerdfs.RootPath(after, ch.After.Self().Dir)
			if err != nil {
				return err
			}
			return MountRef(ctx, newRef, bkSessionGroup, func(root string) (rerr error) {
				beforeMount := filepath.Join(root, "a")
				afterMount := filepath.Join(root, "b")
				if err := os.Mkdir(beforeMount, 0755); err != nil {
					return err
				}
				defer os.RemoveAll(beforeMount)
				if err := os.Mkdir(afterMount, 0755); err != nil {
					return err
				}
				defer os.RemoveAll(afterMount)
				if err := syscall.Mount(beforeDir, beforeMount, "", syscall.MS_BIND, ""); err != nil {
					return fmt.Errorf("mount before to ./a/: %w", err)
				}
				defer syscall.Unmount(beforeMount, syscall.MNT_DETACH)
				if err := syscall.Mount(afterDir, afterMount, "", syscall.MS_BIND, ""); err != nil {
					return fmt.Errorf("mount after to ./b/: %w", err)
				}
				defer syscall.Unmount(afterMount, syscall.MNT_DETACH)

				patchFile, err := os.Create(filepath.Join(root, ChangesetPatchFilename))
				if err != nil {
					return err
				}
				defer patchFile.Close()

				// TODO: once there's an Alpine with git 2.51, we can just pass the
				// paths to git diff --no-index a b -- <all paths>
				diff := func(a, b string) error {
					var path1, path2 string
					if a == "" {
						path1 = "/dev/null"
					} else {
						path1 = filepath.Join("a", a)
					}
					if b == "" {
						path2 = "/dev/null"
					} else {
						path2 = filepath.Join("b", b)
					}
					cmd := exec.Command("git", "diff", "--no-prefix", "--no-index", path1, path2)
					cmd.Dir = root
					cmd.Stdout = io.MultiWriter(patchFile, stdio.Stdout)
					cmd.Stderr = stdio.Stderr
					if err := cmd.Run(); err != nil {
						var exitErr *exec.ExitError
						// Check if it's exit code 1, which is expected for git diff when files differ
						if errors.As(err, &exitErr) && exitErr.ExitCode() != 1 {
							// NB: we could technically populate a buildkit.ExecError here, but that
							// feels like it leaks implementation details; "exit status 128" isn't
							// exactly clear
							return fmt.Errorf("failed to generate patch: %w", err)
						}
					}
					return nil
				}

				for _, modified := range ch.ModifiedPaths {
					if err := diff(modified, modified); err != nil {
						return err
					}
				}
				for _, added := range ch.AddedPaths {
					if strings.HasSuffix(added, "/") {
						continue
					}
					if err := diff("", added); err != nil {
						return err
					}
				}
				for _, removed := range ch.allRemovedPaths {
					if strings.HasSuffix(removed, "/") {
						continue
					}
					if err := diff(removed, ""); err != nil {
						return err
					}
				}
				return nil
			})
		}, mountRefAsReadOnly)
	}, mountRefAsReadOnly)
	if err != nil {
		return nil, err
	}
	snap, err := newRef.Commit(ctx)
	if err != nil {
		return nil, err
	}
	return &File{
		Result:   snap,
		File:     ChangesetPatchFilename,
		Platform: query.Platform(),
	}, nil
}

func (ch *Changeset) Export(ctx context.Context, destPath string) (rerr error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return fmt.Errorf("failed to get buildkit client: %w", err)
	}

	dir, err := ch.Before.Self().DiffLLB(ctx, ch.After.Self())
	if err != nil {
		return err
	}

	var defPB *pb.Definition
	if dir.Dir != "" && dir.Dir != "/" {
		src, err := dir.State()
		if err != nil {
			return err
		}
		src = llb.Scratch().File(llb.Copy(src, dir.Dir, ".", &llb.CopyInfo{
			CopyDirContentsOnly: true,
		}))

		def, err := src.Marshal(ctx, llb.Platform(dir.Platform.Spec()))
		if err != nil {
			return err
		}
		defPB = def.ToPB()
	} else {
		defPB = dir.LLB
	}

	ctx, span := Tracer(ctx).Start(ctx, fmt.Sprintf("export directory %s to host %s", dir.Dir, destPath))
	defer telemetry.End(span, func() error { return rerr })

	return bk.LocalDirExport(ctx, defPB, destPath, true, ch.RemovedPaths)
}
