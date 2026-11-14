package daggercmd

import (
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/stretchr/testify/require"
)

func TestListedArtifactPrunedLinkKeepsCoordinates(t *testing.T) {
	var item listedArtifact
	require.NoError(t, json.Unmarshal([]byte(`{
		"uri":"dag+check://?go-test=TestOne",
		"moduleName":"go",
		"path":["go","packages","tests","test"],
		"dimensionKeys":[
			{"dimension":"module","key":"go"},
			{"dimension":"/go/packages","key":"api/v2"},
			{"dimension":"/go/packages/tests","key":"TestOne"},
			{"dimension":"type:Check","key":"go/packages/tests/test"}
		]
	}`), &item))
	require.NoError(t, item.keysFromAddress(nil))
	require.Equal(t, "go/packages/tests/test", item.fullPath())
	args, err := artifactCLIArguments(item, map[string]string{
		"/go/packages": "go-package", "/go/packages/tests": "go-test", "type:Check": "check",
	}, nil)
	require.NoError(t, err)
	require.Equal(t, "--module=go --go-package=api/v2 --go-test=TestOne --check=go/packages/tests/test", args)
}

func TestListedArtifactCollapsedLinkWithCollectionAlias(t *testing.T) {
	item := listedArtifact{
		URI: "dag+collections-item://?collections-item=a",
		DimensionKeys: []struct{ Dimension, Key string }{
			{"/collections/items", "a"},
			{"/collections/items", "b"},
			{"type:CollectionsItem", "items"},
		},
	}
	uri, err := collapsedArtifactURI(item)
	require.NoError(t, err)
	addr, err := dagaddress.Parse(uri)
	require.NoError(t, err)
	require.Equal(t, []dagaddress.Pair{
		{Dimension: "type:CollectionsItem", Key: "/items", HasKey: true},
		{Dimension: "/collections/items", Key: "a", HasKey: true},
		{Dimension: "/collections/items", Key: "b", HasKey: true},
	}, addr.Query)
}

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
