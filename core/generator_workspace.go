package core

import (
	"context"

	"github.com/dagger/dagger/dagql"
)

func generatorWorkspaceChanges(ctx context.Context, base, generated dagql.ObjectResult[*Workspace]) (dagql.ObjectResult[*Changeset], error) {
	var changes dagql.ObjectResult[*Changeset]
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return changes, err
	}
	id, err := base.ID()
	if err != nil {
		return changes, err
	}
	err = dag.Select(ctx, generated, &changes,
		dagql.Selector{Field: "withWorkdir", Args: []dagql.NamedInput{{Name: "path", Value: dagql.String(".")}}},
		dagql.Selector{Field: "changes", Args: []dagql.NamedInput{{Name: "from", Value: dagql.Opt(dagql.NewID[*Workspace](id))}}},
	)
	return changes, err
}

func generatorChangesAtWorkspaceRoot(ctx context.Context, changes dagql.ObjectResult[*Changeset], cwd string) (dagql.ObjectResult[*Changeset], error) {
	if cwd == "" || cwd == "." {
		return changes, nil
	}
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*Changeset]{}, err
	}
	prefix := func(dir dagql.ObjectResult[*Directory]) (dagql.ObjectResult[*Directory], error) {
		id, err := dir.ID()
		if err != nil {
			return dagql.ObjectResult[*Directory]{}, err
		}
		var rooted dagql.ObjectResult[*Directory]
		err = dag.Select(ctx, dag.Root(), &rooted,
			dagql.Selector{Field: "directory"},
			dagql.Selector{Field: "withDirectory", Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(cwd)},
				{Name: "source", Value: dagql.NewID[*Directory](id)},
			}},
		)
		return rooted, err
	}
	before, err := prefix(changes.Self().Before)
	if err != nil {
		return dagql.ObjectResult[*Changeset]{}, err
	}
	after, err := prefix(changes.Self().After)
	if err != nil {
		return dagql.ObjectResult[*Changeset]{}, err
	}
	beforeID, err := before.ID()
	if err != nil {
		return dagql.ObjectResult[*Changeset]{}, err
	}
	var rooted dagql.ObjectResult[*Changeset]
	err = dag.Select(ctx, after, &rooted, dagql.Selector{
		Field: "changes",
		Args:  []dagql.NamedInput{{Name: "from", Value: dagql.NewID[*Directory](beforeID)}},
	})
	return rooted, err
}
