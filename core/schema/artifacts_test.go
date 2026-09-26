package schema

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"

	"github.com/dagger/dagger/core"
	"github.com/stretchr/testify/require"
)

func TestArtifactDirectiveFilterDoesNotRequireWorkspace(t *testing.T) {
	// Raw metadata filters must work without workspace settings or valid
	// command result types. Command filters enforce those rules separately.
	check := &core.Artifact{Path: []string{"check"}, TypeName: "Container", Directives: []string{"check"}}
	other := &core.Artifact{Path: []string{"other"}, TypeName: "Check"}
	all := &core.Artifacts{Entries: []*core.Artifact{check, other}}
	schema := &artifactsSchema{}
	included, err := schema.filterDirectives(t.Context(), all, artifactDirectiveFilterArgs{Directives: []string{"check"}})
	require.NoError(t, err)
	require.Equal(t, []*core.Artifact{check}, included.Entries)
	excluded, err := schema.filterDirectives(t.Context(), all, artifactDirectiveFilterArgs{Directives: []string{"check"}, Exclude: true})
	require.NoError(t, err)
	require.Equal(t, []*core.Artifact{other}, excluded.Entries)
}

func TestArtifactAbsoluteURIOnEmptySelection(t *testing.T) {
	schema := &artifactsSchema{}
	empty := (&core.Artifacts{Entries: []*core.Artifact{{TypeName: "Container"}}}).FilterTypes([]string{"Check"}, false)
	for name, filter := range map[string]func(context.Context, *core.Artifacts, struct{ URI string }) (*core.Artifacts, error){
		"filterUri":  schema.filterURI,
		"withoutUri": schema.withoutURI,
	} {
		t.Run(name, func(t *testing.T) {
			selected, err := filter(t.Context(), empty, struct{ URI string }{"dag://github.com/acme/project@main:verify"})
			require.NoError(t, err)
			require.Empty(t, selected.Entries)
			_, err = filter(t.Context(), empty, struct{ URI string }{"dag://github.com/acme/project@:verify"})
			require.ErrorContains(t, err, "empty workspace or version")
		})
	}
}

func TestArtifactTypedConversion(t *testing.T) {
	for _, tc := range []struct {
		name       string
		entries    []*core.Artifact
		wantErr    string
		wantValues int
	}{
		{name: "unmarked changeset", entries: []*core.Artifact{{TypeName: "Changeset", Path: []string{"edit"}}}, wantValues: 1},
		{name: "empty"},
		{name: "reject mixed selection before evaluation", entries: []*core.Artifact{
			{TypeName: "Changeset", Path: []string{"edit"}},
			{TypeName: "Service", Path: []string{"serve"}},
		}, wantErr: "dag://serve is a Service, not changeset"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, srv, _, _ := resolverOutputFixture(t)
			srv.InstallObject(dagql.NewClass[*core.Artifacts](srv))
			srv.InstallObject(dagql.NewClass[*core.Artifact](srv))
			srv.InstallObject(dagql.NewClass[*core.Changeset](srv))
			schema := &artifactsSchema{}
			dagql.Fields[*core.Artifacts]{
				dagql.Func("items", schema.items),
				dagql.NodeFunc("asChangesets", schema.asChangesets),
			}.Install(srv)
			evaluations := 0
			dagql.Fields[*core.Artifact]{dagql.Func("value", func(context.Context, *core.Artifact, struct{}) (*core.Changeset, error) {
				evaluations++
				return &core.Changeset{}, nil
			})}.Install(srv)
			dagql.Fields[*core.Query]{dagql.Func("selection", func(context.Context, *core.Query, struct{}) (*core.Artifacts, error) {
				return &core.Artifacts{Entries: tc.entries}, nil
			})}.Install(srv)
			var result dagql.ObjectResultArray[*core.Changeset]
			err := srv.Select(ctx, srv.Root(), &result, dagql.Selector{Field: "selection"}, dagql.Selector{Field: "asChangesets"})
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
				require.Len(t, result, tc.wantValues)
			}
			require.Equal(t, tc.wantValues, evaluations)
		})
	}
}
