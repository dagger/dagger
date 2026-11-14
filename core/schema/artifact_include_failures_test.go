package schema

import (
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/stretchr/testify/require"
)

func TestArtifactIncludesFilterModuleLoadFailures(t *testing.T) {
	alpha := &core.Artifact{ModuleName: "alpha", TypeName: "Check", Path: []string{"alpha", "verify"}}
	beta := &core.Artifact{ModuleName: "beta", TypeName: "Check", Path: []string{"beta", "load"},
		LoadFailure: &core.ModuleLoadFailure{Name: "beta", Message: "beta failed"}}
	for _, tc := range []struct {
		name    string
		include []string
		want    []*core.Artifact
	}{
		{"healthy module only", []string{"alpha"}, []*core.Artifact{alpha}},
		{"field under failed module", []string{"beta:verify"}, []*core.Artifact{beta}},
		{"include alternatives", []string{"missing", "beta/verify"}, []*core.Artifact{beta}},
		{"omitted includes", nil, []*core.Artifact{alpha, beta}},
		{"empty includes", []string{}, []*core.Artifact{alpha, beta}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected := []*core.Artifact{}
			for _, entry := range []*core.Artifact{alpha, beta} {
				match, err := matchLoadFailureInclude(entry, tc.include)
				require.NoError(t, err)
				if match {
					selected = append(selected, entry)
				}
			}
			require.Equal(t, tc.want, selected)
		})
	}
}

func TestArtifactIncludesKeepUnknownMainFields(t *testing.T) {
	failure := &core.Artifact{ModuleName: "beta", TypeName: "Check", Path: []string{"beta", "load"},
		LoadFailure: &core.ModuleLoadFailure{Name: "beta", Entrypoint: true, Siblings: []string{"alpha"}}}
	for _, tc := range []struct {
		include string
		want    bool
	}{{"verify", true}, {"beta:verify", true}, {"alpha:verify", false}, {"*:verify", true}} {
		t.Run(tc.include, func(t *testing.T) {
			match, err := matchLoadFailureInclude(failure, []string{tc.include})
			require.NoError(t, err)
			require.Equal(t, tc.want, match)
		})
	}
}
