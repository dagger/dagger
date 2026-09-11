package core

import (
	"fmt"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceMigrationRetainsChangesets(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	srv, err := dagql.NewServer(ctx, &Query{})
	require.NoError(t, err)
	srv.InstallObject(dagql.NewClass[*Changeset](srv))
	newChanges := func(name string) dagql.ObjectResult[*Changeset] {
		res, err := dagql.NewObjectResultForCall(&Changeset{}, srv, &dagql.ResultCall{
			Kind: dagql.ResultCallKindSynthetic, SyntheticOp: name,
			Type: dagql.NewResultCallType((&Changeset{}).Type()),
		})
		require.NoError(t, err)
		return res
	}
	plan := &WorkspaceMigration{
		Changes: newChanges("plan"),
		Steps: []*WorkspaceMigrationStep{
			{Changes: newChanges("workspace")},
			{Changes: newChanges("modules")},
		},
	}
	var attached []dagql.ObjectResult[*Changeset]
	deps, err := plan.AttachDependencyResults(ctx, nil, func(res dagql.AnyResult) (dagql.AnyResult, error) {
		_, ok := res.(dagql.ObjectResult[*Changeset])
		require.True(t, ok)
		next := newChanges(fmt.Sprintf("attached-%d", len(attached)))
		attached = append(attached, next)
		return next, nil
	})
	require.NoError(t, err)
	require.Len(t, deps, 3)
	require.Same(t, attached[0].Self(), plan.Changes.Self())
	for i, step := range plan.Steps {
		require.Same(t, attached[i+1].Self(), step.Changes.Self())
	}
	for i, dep := range deps {
		require.Same(t, attached[i].Self(), dep.(dagql.ObjectResult[*Changeset]).Self())
	}
}
