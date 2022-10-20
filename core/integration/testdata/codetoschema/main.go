package main

import (
	"context"
	"fmt"

	"dagger.io/dagger"
)

func main() {
	dagger.Serve(Test{})
}

type Test struct {
}

func (Test) RequiredTypes(
	ctx context.Context,
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

func (Test) OptionalTypes(
	ctx context.Context,
	str *string,
	i *int,
	b *bool,
	strArray []*string,
	intArray []*int,
	boolArray []*bool,
) (string, error) {
	return fmt.Sprintf("%s %s %s %s %s %s", ptrFormat(str), ptrFormat(i), ptrFormat(b), ptrArrayFormat(strArray), ptrArrayFormat(intArray), ptrArrayFormat(boolArray)), nil
}

func (Test) OptionalReturn(ctx context.Context, returnNil bool) (*string, error) {
	if returnNil {
		return nil, nil
	}
	s := "not nil"
	return &s, nil
}

func (Test) IntArrayReturn(ctx context.Context, intArray []int) ([]int, error) {
	return intArray, nil
}

func (Test) StringArrayReturn(ctx context.Context, strArray []*string) ([]*string, error) {
	return strArray, nil
}

func (Test) StructReturn(ctx context.Context, strukt AllTheTypes) (AllTheTypes, error) {
	return strukt, nil
}

func (Test) ParentResolver(ctx context.Context, str string) (SubResolver, error) {
	return SubResolver{Str: str}, nil
}

type SubResolver struct {
	Str string
}

func (s SubResolver) SubField(ctx context.Context, str string) (string, error) {
	return s.Str + "-" + str, nil
}

func (Test) ReturnDirectory(ctx context.Context, ref string) (*dagger.Directory, error) {
	client, err := dagger.Connect(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	return client.Container().From(ref).FS(), nil
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
