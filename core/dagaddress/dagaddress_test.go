package dagaddress

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  Address
		str   string
	}{
		{
			value: "",
			want:  Address{},
			str:   "dag://",
		},
		{
			value: "dag://",
			want:  Address{HasScheme: true},
			str:   "dag://",
		},
		{
			value: "golang/modules/tests/container",
			want:  Address{Path: "golang/modules/tests/container"},
			str:   "dag://golang/modules/tests/container",
		},
		{
			value: "golang:modules:tests:container",
			want:  Address{Path: "golang/modules/tests/container"},
			str:   "dag://golang/modules/tests/container",
		},
		{
			value: "dag://golang/modules/tests/container?go-module=sdk/go&go-test=TestConnect",
			want: Address{HasScheme: true, Path: "golang/modules/tests/container", Query: []Pair{
				{Dimension: "go-module", Key: "sdk/go", HasKey: true},
				{Dimension: "go-test", Key: "TestConnect", HasKey: true},
			}},
			str: "dag://golang/modules/tests/container?go-module=sdk/go&go-test=TestConnect",
		},
		{
			value: "dag+container://golang/modules/tests/container",
			want:  Address{HasScheme: true, Types: []string{"container"}, Path: "golang/modules/tests/container"},
			str:   "dag+container://golang/modules/tests/container",
		},
		{
			value: "dag+container+directory://",
			want:  Address{HasScheme: true, Types: []string{"container", "directory"}},
			str:   "dag+container+directory://",
		},
		{
			value: "dag://github.com/dagger/dagger@main:golang/modules/tests/container?go-module=sdk/go",
			want: Address{
				HasScheme: true, Absolute: true, Workspace: "github.com/dagger/dagger", Version: "main",
				Path:  "golang/modules/tests/container",
				Query: []Pair{{Dimension: "go-module", Key: "sdk/go", HasKey: true}},
			},
			str: "dag://github.com/dagger/dagger@main:golang/modules/tests/container?go-module=sdk/go",
		},
		{
			// A ref can contain "/" but not ":", so "@<version>:" is an exact split.
			// Later colons in the path are the old separator.
			value: "dag://github.com/dagger/dagger@release/v1:golang:modules:test",
			want: Address{
				HasScheme: true, Absolute: true, Workspace: "github.com/dagger/dagger", Version: "release/v1",
				Path: "golang/modules/test",
			},
			str: "dag://github.com/dagger/dagger@release/v1:golang/modules/test",
		},
		{
			value: "dag://github.com/dagger/dagger@c624e1f:",
			want:  Address{HasScheme: true, Absolute: true, Workspace: "github.com/dagger/dagger", Version: "c624e1f"},
			str:   "dag://github.com/dagger/dagger@c624e1f:",
		},
		{
			// Neither separator splits a key; percent-decoding applies to keys.
			value: "dag://tests?go-test=a:b/c&go-test=x%20y%2Bz%26w&go-module",
			want: Address{HasScheme: true, Path: "tests", Query: []Pair{
				{Dimension: "go-test", Key: "a:b/c", HasKey: true},
				{Dimension: "go-test", Key: "x y+z&w", HasKey: true},
				{Dimension: "go-module"},
			}},
			str: "dag://tests?go-test=a:b/c&go-test=x%20y%2Bz%26w&go-module",
		},
		{
			value: "{docs/**,base}",
			want:  Address{Path: "{docs/**,base}"},
			str:   "dag://{docs/**,base}",
		},
		{
			value: "dag://?go-module=sdk/go",
			want:  Address{HasScheme: true, Query: []Pair{{Dimension: "go-module", Key: "sdk/go", HasKey: true}}},
			str:   "dag://?go-module=sdk/go",
		},
		{
			value: "tests?source=https://example.com/repo",
			want: Address{Path: "tests", Query: []Pair{
				{Dimension: "source", Key: "https://example.com/repo", HasKey: true},
			}},
			str: "dag://tests?source=https://example.com/repo",
		},
		{
			value: "?source=dag://provider/image",
			want: Address{Query: []Pair{
				{Dimension: "source", Key: "dag://provider/image", HasKey: true},
			}},
			str: "dag://?source=dag://provider/image",
		},
	} {
		t.Run(tc.value, func(t *testing.T) {
			got, err := Parse(tc.value)
			require.NoError(t, err)
			require.Equal(t, tc.want, *got)
			require.Equal(t, tc.str, got.String())
			again, err := Parse(got.String())
			require.NoError(t, err)
			again.HasScheme = tc.want.HasScheme
			require.Equal(t, tc.want, *again)
		})
	}
}

func TestParseErrors(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"https://github.com/dagger/dagger", "not a DAG address"},
		{"docker://alpine", "not a DAG address"},
		{"dag+://base", "empty type in scheme"},
		{"dag://github.com/dagger/dagger@main", "tree addresses are not supported"},
		{"dag://@main:base", "empty workspace or version"},
		{"dag://base?=key", "empty dimension"},
		{"dag://base?go-test=%zz", "invalid URL escape"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			_, err := Parse(tc.value)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestDimensionFilters(t *testing.T) {
	addr, err := Parse("dag://tests?go-test=a&go-module=sdk/go&go-test=b&other&other=x")
	require.NoError(t, err)
	require.Equal(t, []DimensionFilter{
		{Dimension: "go-test", Keys: []string{"a", "b"}},
		{Dimension: "go-module", Keys: []string{"sdk/go"}},
		{Dimension: "other", Keys: nil},
	}, addr.DimensionFilters())
}

func TestIsAddress(t *testing.T) {
	require.True(t, IsAddress("dag://base"))
	require.True(t, IsAddress("dag+container://base"))
	require.False(t, IsAddress("base"))
	require.False(t, IsAddress("provider:base"))
	require.False(t, IsAddress("alpine:3.20"))
	require.False(t, IsAddress("tcp://localhost:8080"))
	require.False(t, IsAddress("dagger://x"))
}
