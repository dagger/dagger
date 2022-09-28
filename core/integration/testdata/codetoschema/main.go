package main

import (
	"fmt"

	"github.com/Khan/genqlient/graphql"
	"go.dagger.io/dagger/sdk/go/dagger"
)

func main() {
	dagger.Serve(Test{})
}

type Test struct {
}

func (Test) A(
	ctx dagger.Context,
	str string,
	i int,
	b bool,
	strukt AllTheTypes,
	strArray []string,
	intArray []int,
	boolArray []bool,
) (string, error) {
	return fmt.Sprintf("%s %d %t %+v %v %v %v", str, i, b, strukt, strArray, intArray, boolArray), nil
}

func (Test) B(
	ctx dagger.Context,
	str *string,
	i *int,
	b *bool,
	strArray []*string,
	intArray []*int,
	boolArray []*bool,
) (string, error) {
	return fmt.Sprintf("%s %s %s %s %s %s", ptrFormat(str), ptrFormat(i), ptrFormat(b), ptrArrayFormat(strArray), ptrArrayFormat(intArray), ptrArrayFormat(boolArray)), nil
}

func (Test) C(ctx dagger.Context, returnNil bool) (*string, error) {
	if returnNil {
		return nil, nil
	}
	s := "not nil"
	return &s, nil
}

func (Test) D(ctx dagger.Context, intArray []int) ([]int, error) {
	return intArray, nil
}

func (Test) E(ctx dagger.Context, strArray []*string) ([]*string, error) {
	return strArray, nil
}

func (Test) F(ctx dagger.Context, strukt AllTheTypes) (AllTheTypes, error) {
	return strukt, nil
}

func (Test) G(ctx dagger.Context, str string) (SubResolver, error) {
	return SubResolver{Str: str}, nil
}

func (Test) H(ctx dagger.Context, ref string) (*dagger.Filesystem, error) {
	client, err := dagger.Client(ctx)
	if err != nil {
		return nil, err
	}

	req := &graphql.Request{
		Query: `
query Image ($ref: String!) {
	core {
		image(ref: $ref) {
			id
		}
	}
}
`,
		Variables: map[string]any{
			"ref": ref,
		},
	}
	resp := struct {
		Core struct {
			Image dagger.Filesystem
		}
	}{}
	err = client.MakeRequest(ctx, req, &graphql.Response{Data: &resp})
	if err != nil {
		return nil, err
	}

	return &resp.Core.Image, nil
}

type SubResolver struct {
	Str string
}

func (s SubResolver) SubField(ctx dagger.Context, str string) (string, error) {
	return s.Str + "-" + str, nil
}

type AllTheTypes struct {
	Str       string
	Int       int
	Bool      bool
	StrArray  []string
	IntArray  []int
	BoolArray []bool
	SubStruct AllTheSubTypes
}

type AllTheSubTypes struct {
	SubStr       string
	SubInt       int
	SubBool      bool
	SubStrArray  []string
	SubIntArray  []int
	SubBoolArray []bool
}

func ptrFormat[T any](p *T) string {
	if p == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%v", *p)
}

func ptrArrayFormat[T any](s []*T) string {
	if s == nil {
		return "<nil>"
	}
	var out []string
	for _, str := range s {
		out = append(out, ptrFormat(str))
	}
	return fmt.Sprintf("%v", out)
}
