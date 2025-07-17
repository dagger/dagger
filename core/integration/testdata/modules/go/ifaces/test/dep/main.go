package main

import (
	"context"
	"dagger/dep/internal/dagger"
)

type Dep struct {
	IfaceField CustomIface
}

type CustomIface interface {
	DaggerObject
	Void(ctx context.Context) error

	Str(ctx context.Context) (string, error)
	WithStr(ctx context.Context, strArg string) CustomIface
	WithOptionalPragmaStr(
		ctx context.Context,
		// +optional
		strArg string,
	) CustomIface
	StrList(ctx context.Context) ([]string, error)
	WithStrList(ctx context.Context, strListArg []string) CustomIface

	Int(ctx context.Context) (int, error)
	WithInt(ctx context.Context, intArg int) CustomIface
	IntList(ctx context.Context) ([]int, error)
	WithIntList(ctx context.Context, intListArg []int) CustomIface

	Bool(ctx context.Context) (bool, error)
	WithBool(ctx context.Context, boolArg bool) CustomIface
	BoolList(ctx context.Context) ([]bool, error)
	WithBoolList(ctx context.Context, boolListArg []bool) CustomIface

	Obj() *dagger.Directory
	WithObj(objArg *dagger.Directory) CustomIface
	WithOptionalPragmaObj(
		// +optional
		objArg *dagger.Directory,
	) CustomIface
	ObjList(ctx context.Context) ([]*dagger.Directory, error)
	WithObjList(ctx context.Context, objListArg []*dagger.Directory) CustomIface

	SelfIface() CustomIface
	SelfIfaceList(ctx context.Context) ([]CustomIface, error)

	OtherIface() OtherIface
	StaticOtherIfaceList(ctx context.Context) ([]OtherIface, error)

	WithOtherIface(other OtherIface) CustomIface
	DynamicOtherIfaceList(ctx context.Context) ([]OtherIface, error)

	WithOtherIfaceByIface(other OtherIface) CustomIface
	DynamicOtherIfaceByIfaceList(ctx context.Context) ([]OtherIface, error)
}

type OtherIface interface {
	DaggerObject
	Foo(ctx context.Context) (string, error)
}

func (m *Dep) WithIface(ctx context.Context, iface CustomIface) (*Dep, error) {
	m.IfaceField = iface
	return m, nil
}

func (m *Dep) IfaceFieldStr(ctx context.Context) (string, error) {
	return m.IfaceField.Str(ctx)
}
