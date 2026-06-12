package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNamespaceObjects(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		testCase  string
		namespace string
		obj       string
		result    string
	}{
		{
			testCase:  "namespace",
			namespace: "Foo",
			obj:       "Bar",
			result:    "FooBar",
		},
		{
			testCase:  "namespace into camel case",
			namespace: "foo",
			obj:       "bar-baz",
			result:    "FooBarBaz",
		},
		{
			testCase:  "don't namespace when equal",
			namespace: "foo",
			obj:       "Foo",
			result:    "Foo",
		},
		{
			testCase:  "don't namespace when prefixed",
			namespace: "foo",
			obj:       "FooBar",
			result:    "FooBar",
		},
		{
			testCase:  "still namespace when prefixed if not full",
			namespace: "foo",
			obj:       "Foobar",
			result:    "FooFoobar",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.testCase, func(t *testing.T) {
			result := namespaceObject(tc.obj, tc.namespace, tc.namespace)
			require.Equal(t, tc.result, result)
		})
	}
}

// TestTypeDefWithNameVerbatim guards the invariant that the internal __withName
// rename stores an already-final GraphQL name as-is. namespaceObject produces
// the final namespaced name; re-normalizing it here (strcase.ToCamel is not
// idempotent) corrupted already-cased multi-word names, e.g. turning
// "ModuleAOverlay" into "ModuleAoverlay".
func TestTypeDefWithNameVerbatim(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"ModuleAOverlay", "ModuleBFileOverlay"} {
		require.Equal(t, name, (&ObjectTypeDef{}).WithName(name).Name)
		require.Equal(t, name, (&InterfaceTypeDef{}).WithName(name).Name)
		require.Equal(t, name, (&EnumTypeDef{}).WithName(name).Name)
	}
}
