package main

import "context"

func New(
	strs []string,
	ints []int,
	bools []bool,
	dirs []*Directory,
) *Impl {

	return &Impl{
		Str:     strs[0],
		StrList: strs,

		Int:     ints[0],
		IntList: ints,

		Bool:     bools[0],
		BoolList: bools,

		Obj:     dirs[0],
		ObjList: dirs,
	}
}

type Impl struct {
	Str     string
	StrList []string

	Int     int
	IntList []int

	Bool     bool
	BoolList []bool

	Obj     *Directory
	ObjList []*Directory

	Others      []*OtherImpl
	OtherIfaces []LocalOtherIface
}

func (m *Impl) Void() error {
	return nil
}

func (m Impl) WithStr(strArg string) *Impl {
	m.Str = strArg
	return &m
}

func (m Impl) WithOptionalPragmaStr(
	// +optional
	strArg string,
) *Impl {
	if strArg != "" {
		m.Str = strArg
	}
	return &m
}

func (m Impl) WithStrList(strListArg []string) *Impl {
	m.StrList = strListArg
	return &m
}

func (m Impl) WithInt(intArg int) *Impl {
	m.Int = intArg
	return &m
}

func (m Impl) WithIntList(intListArg []int) *Impl {
	m.IntList = intListArg
	return &m
}

func (m Impl) WithBool(boolArg bool) *Impl {
	m.Bool = boolArg
	return &m
}

func (m Impl) WithBoolList(boolListArg []bool) *Impl {
	m.BoolList = boolListArg
	return &m
}

func (m Impl) WithObj(objArg *Directory) *Impl {
	m.Obj = objArg
	return &m
}

func (m Impl) WithOptionalPragmaObj(
	// +optional
	objArg *Directory,
) *Impl {
	if objArg != nil {
		m.Obj = objArg
	}
	return &m
}

func (m Impl) WithObjList(objListArg []*Directory) *Impl {
	m.ObjList = objListArg
	return &m
}

func (m *Impl) SelfIface() *Impl {
	return m.WithStr(m.Str + "self")
}

func (m *Impl) SelfIfaceList() []*Impl {
	return []*Impl{
		m.WithStr(m.Str + "self1"),
		m.WithStr(m.Str + "self2"),
	}
}

func (m *Impl) OtherIface() *OtherImpl {
	return &OtherImpl{Foo: m.Str + "other"}
}

func (m *Impl) StaticOtherIfaceList() []*OtherImpl {
	return []*OtherImpl{
		{Foo: m.Str + "other1"},
		{Foo: m.Str + "other2"},
	}
}

func (m *Impl) WithOtherIface(other *OtherImpl) *Impl {
	m.Others = append(m.Others, other)
	return m
}

func (m *Impl) DynamicOtherIfaceList() []*OtherImpl {
	return m.Others
}

func (m *Impl) WithOtherIfaceByIface(other LocalOtherIface) *Impl {
	m.OtherIfaces = append(m.OtherIfaces, other)
	return m
}

func (m *Impl) DynamicOtherIfaceByIfaceList() []LocalOtherIface {
	return m.OtherIfaces
}

type OtherImpl struct {
	Foo string
}

// LocalOtherIface is the same as OtherIface and is used here to test interface
// to interface compatibility.
type LocalOtherIface interface {
	DaggerObject
	Foo(ctx context.Context) (string, error)
}
