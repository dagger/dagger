package daggercmd

import (
	"testing"

	"github.com/dagger/dagger/core/artifact"
	"github.com/stretchr/testify/require"
)

func TestListedArtifactKeysFromAddress(t *testing.T) {
	definitions := artifact.Dimensions{
		{Kind: "TYPE", Identifier: "type:Check", Name: "check", QualifiedName: "artifact-check"},
		{Kind: "COLLECTION", Identifier: "/go/packages", Name: "go-package", QualifiedName: "go-packages-package"},
		{Kind: "COLLECTION", Identifier: "/go/packages/tests", Name: "go-test", QualifiedName: "go-packages-tests-test"},
	}
	for _, tc := range []struct {
		name, uri, module string
		path              []string
		want              []struct{ Dimension, Key string }
	}{
		{"short type key", "dag+check://?check=test&go-package=api%2Fv2&go-test=TestOne", "go", []string{"go", "packages", "tests", "test"}, []struct{ Dimension, Key string }{
			{"module", "go"}, {"/go/packages", "api/v2"}, {"/go/packages/tests", "TestOne"}, {"type:Check", "go/packages/tests/test"},
		}},
		{"empty and escaped keys", "dag://?artifact-check=test&go-package=&go-test=%E6%B5%8B%E8%AF%95%26%2F%2B", "go", []string{"go", "test"}, []struct{ Dimension, Key string }{
			{"module", "go"}, {"/go/packages", ""}, {"/go/packages/tests", "测试&/+"}, {"type:Check", "go/test"},
		}},
		{"static check", "dag://?check=app%2Flint", "app", []string{"app", "lint"}, []struct{ Dimension, Key string }{
			{"module", "app"}, {"type:Check", "app/lint"},
		}},
		{"bound check", "dag://?check=lint", "", []string{"lint"}, []struct{ Dimension, Key string }{
			{"type:Check", "lint"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := listedArtifact{URI: tc.uri, ModuleName: tc.module, Path: tc.path}
			require.NoError(t, item.keysFromAddress(definitions))
			require.Equal(t, tc.want, item.DimensionKeys)
			require.Equal(t, tc.uri, item.URI, "formatting keeps the engine's canonical address")
		})
	}
}
