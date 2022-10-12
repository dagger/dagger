package querybuilder

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMarshalGQL(t *testing.T) {
	var (
		str         = "hello world"
		strNullPtr  *string
		strPtrSlice = []*string{&str}
	)

	testCases := []struct {
		v      any
		expect string
	}{
		{
			v:      str,
			expect: "\"hello world\"",
		},
		{
			v:      &str,
			expect: "\"hello world\"",
		},
		{
			v:      strNullPtr,
			expect: "null",
		},
		{
			v:      42,
			expect: "42",
		},
		{
			v:      true,
			expect: "true",
		},
		// FIXME
		// {
		// 	v:      nil,
		// 	expect: "null",
		// },
		// {
		// 	v:      []*string{nil},
		// 	expect: "",
		// },
		{
			v:      []string{"1", "2", "3"},
			expect: `["1","2","3"]`,
		},
		{
			v:      strPtrSlice,
			expect: `["hello world"]`,
		},
		{
			v:      &strPtrSlice,
			expect: `["hello world"]`,
		},
	}

	for _, testCase := range testCases {
		require.Equal(t, testCase.expect, MarshalGQL(testCase.v))
	}
}

func TestMarshalGQLStruct(t *testing.T) {
	s := struct {
		A   string `json:"a,omitempty"`
		B   int    `json:"b"`
		Sub struct {
			X []string `json:"x"`
		} `json:"sub"`
	}{
		A: "test",
		B: 42,
	}
	s.Sub.X = []string{"1"}
	require.Equal(t, `{a:"test",b:42,sub:{x:["1"]}}`, MarshalGQL(s))
}

func TestIsZeroValue(t *testing.T) {
	zero := []any{
		"",
		0,
		[]string{},
		struct {
			Foo string
		}{},
	}

	nonZero := []any{
		"hello",
		42,
		[]string{"world"},
		struct {
			Foo string
		}{
			Foo: "bar",
		},
	}

	for _, i := range zero {
		require.True(t, IsZeroValue(i), fmt.Sprintf("%v", i))
	}

	for _, i := range nonZero {
		require.False(t, IsZeroValue(i), fmt.Sprintf("%v", i))
	}
}
