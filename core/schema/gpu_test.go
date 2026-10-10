package schema

import (
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

// withGPU appears in the v1.0 API, which every v1.0.0 prerelease caller gets.
// The experimental names stay in every view, deprecated only where withGPU
// exists. Versions are declared ones, mapped to views as the engine does.
func TestGPUSchemaVersions(t *testing.T) {
	for _, tc := range []struct {
		version    string
		hasWithGPU bool
	}{
		{version: "v0.21.0"},
		{version: "v1.0.0-beta.12", hasWithGPU: true},
		{version: "v1.0.0-beta.14", hasWithGPU: true},
		{version: "v1.0.0-beta.15", hasWithGPU: true},
		{version: "v1.0.0-0", hasWithGPU: true},
		{version: "v1.0.0", hasWithGPU: true},
	} {
		t.Run(tc.version, func(t *testing.T) {
			_, dag := newNestingTestServer(t, call.View(engine.APIViewVersion(tc.version)))
			data, err := getSchemaJSON(nil, nil, dag.View, dag)
			require.NoError(t, err)
			ctr := decodeSchemaResponse(t, data).Schema.Types.Get("Container")
			require.NotNil(t, ctr)

			withGPU := schemaField(ctr, "withGPU")
			require.Equal(t, tc.hasWithGPU, withGPU != nil)
			if withGPU != nil {
				require.Empty(t, withGPU.Args)
				require.False(t, withGPU.IsDeprecated)
			}

			for _, name := range []string{"experimentalWithGPU", "experimentalWithAllGPUs"} {
				field := schemaField(ctr, name)
				require.NotNil(t, field, name)
				require.Equal(t, tc.hasWithGPU, field.IsDeprecated, name)
			}
			require.Len(t, schemaField(ctr, "experimentalWithGPU").Args, 1)
		})
	}
}

// Every spelling sets the same container state.
func TestGPUCallState(t *testing.T) {
	ctx, dag := newNestingTestServer(t, "v1.0.0")
	for _, tc := range []struct {
		field string
		view  call.View
		args  []dagql.NamedInput
		want  []string
	}{
		{field: "withGPU", want: []string{"all"}},
		{field: "experimentalWithAllGPUs", want: []string{"all"}},
		{field: "experimentalWithAllGPUs", view: "v0.21.0", want: []string{"all"}},
		{field: "experimentalWithGPU", args: []dagql.NamedInput{
			{Name: "devices", Value: dagql.ArrayInput[dagql.String]{"GPU-a", "GPU-b"}},
		}, want: []string{"GPU-a", "GPU-b"}},
		{field: "experimentalWithGPU", view: "v0.21.0", args: []dagql.NamedInput{
			{Name: "devices", Value: dagql.ArrayInput[dagql.String]{"GPU-a"}},
		}, want: []string{"GPU-a"}},
	} {
		t.Run(tc.field+"/"+string(tc.view), func(t *testing.T) {
			var ctr dagql.ObjectResult[*core.Container]
			require.NoError(t, dag.Select(ctx, dag.Root(), &ctr,
				dagql.Selector{Field: "container"},
				dagql.Selector{Field: tc.field, View: tc.view, Args: tc.args},
			))
			require.Equal(t, tc.want, ctr.Self().EnabledGPUs)
		})
	}
}
