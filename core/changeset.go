package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/moby/buildkit/solver/pb"
	"github.com/vektah/gqlparser/v2/ast"
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
			ch.ChangedPaths = append(ch.ChangedPaths, path)
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

	return nil
}

type Changeset struct {
	Before dagql.ObjectResult[*Directory] `field:"true" doc:"The older/lower snapshot to compare against."`
	After  dagql.ObjectResult[*Directory] `field:"true" doc:"The newer/upper snapshot."`

	AddedPaths   []string `field:"true" doc:"Files and directories that were added in the newer directory."`
	ChangedPaths []string `field:"true" doc:"Files and directories that existed before and were updated in the newer directory."`
	RemovedPaths []string `field:"true" doc:"Files and directories that were removed. Directories are indicated by a trailing slash, and their child paths are not included."`
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
