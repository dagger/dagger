package querybuilder

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

type enumType string

func (c enumType) IsEnum()       {}
func (c enumType) Name() string  { return string(c) }
func (c enumType) Value() string { return string(c) }

var _ enum = enumType("")

func TestMarshalGQL(t *testing.T) {
	var (
		str         = "hello world"
		unicode     = "∆?–∂∂√˛viÙ˜Ÿ¿GÆÓ∂Ó˘◊ñ" //nolint:staticcheck
		strNullPtr  *string
		strPtrSlice          = []*string{&str}
		enumVal     enumType = "test"
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
		{
			v:      unicode,
			expect: "\"∆?–∂∂√˛\\u0007v\\u001CiÙ˜Ÿ¿\\u0011GÆÓ∂Ó˘◊ñ\"",
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
		{
			v:      enumVal,
			expect: "test",
		},
	}

	for _, testCase := range testCases {
		enc, err := MarshalGQL(context.TODO(), testCase.v)
		require.NoError(t, err)
		require.Equal(t, testCase.expect, enc)
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
	enc, err := MarshalGQL(context.TODO(), s)
	require.NoError(t, err)
	require.Equal(t, `{a:"test",b:42,sub:{x:["1"]}}`, enc)
}

type customMarshaller struct {
	v     string
	count int
}

//nolint:staticcheck
func (m *customMarshaller) XXX_GraphQLType() string { return "idTest" }

//nolint:staticcheck
func (m *customMarshaller) XXX_GraphQLIDType() string { return "idTypeTest" }

//nolint:staticcheck
func (m *customMarshaller) XXX_GraphQLID(context.Context) (string, error) {
	m.count++
	return m.v, nil
}

func (m *customMarshaller) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		V     string `json:"v"`
		Count int    `json:"count"`
	}{m.v, m.count})
}

var _ GraphQLMarshaller = &customMarshaller{}

func TestCustomMarshaller(t *testing.T) {
	testCases := []struct {
		v      any
		expect string
	}{
		{
			v:      &customMarshaller{v: "custom"},
			expect: `"custom"`,
		},
		{
			v: []*customMarshaller{
				{v: "custom1"},
				{v: "custom2"},
			},
			expect: `["custom1","custom2"]`,
		},
	}

	for _, testCase := range testCases {
		enc, err := MarshalGQL(context.TODO(), testCase.v)
		require.NoError(t, err)
		require.Equal(t, testCase.expect, enc)
	}
}

func TestIsZeroValue(t *testing.T) {
	// emptyPtr covers the case of nil reflect.Pointer:
	var emptyPtr *string

	zero := []any{
		"",
		0,
		[]string{},
		struct {
			Foo string
		}{},
		emptyPtr,
	}

	stringPtr := "test"
	nonZero := []any{
		"hello",
		42,
		[]string{"world"},
		struct {
			Foo string
		}{
			Foo: "bar",
		},
		&stringPtr,
	}

	for _, i := range zero {
		require.True(t, IsZeroValue(i), fmt.Sprintf("%v", i))
	}

	for _, i := range nonZero {
		require.False(t, IsZeroValue(i), fmt.Sprintf("%v", i))
	}
}
