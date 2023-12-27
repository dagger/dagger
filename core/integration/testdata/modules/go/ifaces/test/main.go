package main

import (
	"context"
	"fmt"
)

type Test struct {
	Iface          CustomIface
	IfaceList      []CustomIface
	OtherIfaceList []OtherIface
}

type CustomIface interface {
	DaggerObject
	Void(ctx context.Context) error

	Str(ctx context.Context) (string, error)
	WithStr(ctx context.Context, strArg string) CustomIface
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

	Obj() *Directory
	WithObj(objArg *Directory) CustomIface
	ObjList(ctx context.Context) ([]*Directory, error)
	WithObjList(ctx context.Context, objListArg []*Directory) CustomIface

	SelfIface() CustomIface
	SelfIfaceList(ctx context.Context) ([]CustomIface, error)

	OtherIface() OtherIface
	OtherIfaceList(ctx context.Context) ([]OtherIface, error)
}

type OtherIface interface {
	DaggerObject
	Foo(ctx context.Context) (string, error)
}

func (m *Test) TestVoid(ctx context.Context, ifaceArg CustomIface) error {
	return ifaceArg.Void(ctx)
}

func (m *Test) TestStr(ctx context.Context, ifaceArg CustomIface) (string, error) {
	return ifaceArg.Str(ctx)
}

func (m *Test) TestWithStr(ctx context.Context, ifaceArg CustomIface, strArg string) CustomIface {
	return ifaceArg.WithStr(ctx, strArg)
}

func (m *Test) TestStrList(ctx context.Context, ifaceArg CustomIface) ([]string, error) {
	return ifaceArg.StrList(ctx)
}

func (m *Test) TestWithStrList(ctx context.Context, ifaceArg CustomIface, strList []string) CustomIface {
	return ifaceArg.WithStrList(ctx, strList)
}

func (m *Test) TestInt(ctx context.Context, ifaceArg CustomIface) (int, error) {
	return ifaceArg.Int(ctx)
}

func (m *Test) TestWithInt(ctx context.Context, ifaceArg CustomIface, intArg int) CustomIface {
	return ifaceArg.WithInt(ctx, intArg)
}

func (m *Test) TestIntList(ctx context.Context, ifaceArg CustomIface) ([]int, error) {
	return ifaceArg.IntList(ctx)
}

func (m *Test) TestWithIntList(ctx context.Context, ifaceArg CustomIface, intList []int) CustomIface {
	return ifaceArg.WithIntList(ctx, intList)
}

func (m *Test) TestBool(ctx context.Context, ifaceArg CustomIface) (bool, error) {
	return ifaceArg.Bool(ctx)
}

func (m *Test) TestWithBool(ctx context.Context, ifaceArg CustomIface, boolArg bool) CustomIface {
	return ifaceArg.WithBool(ctx, boolArg)
}

func (m *Test) TestBoolList(ctx context.Context, ifaceArg CustomIface) ([]bool, error) {
	return ifaceArg.BoolList(ctx)
}

func (m *Test) TestWithBoolList(ctx context.Context, ifaceArg CustomIface, boolList []bool) CustomIface {
	return ifaceArg.WithBoolList(ctx, boolList)
}

func (m *Test) TestObj(ifaceArg CustomIface) *Directory {
	return ifaceArg.Obj()
}

func (m *Test) TestWithObj(ifaceArg CustomIface, objArg *Directory) CustomIface {
	return ifaceArg.WithObj(objArg)
}

func (m *Test) TestObjList(ctx context.Context, ifaceArg CustomIface) ([]*Directory, error) {
	return ifaceArg.ObjList(ctx)
}

func (m *Test) TestWithObjList(ctx context.Context, ifaceArg CustomIface, objList []*Directory) CustomIface {
	return ifaceArg.WithObjList(ctx, objList)
}

func (m *Test) TestSelfIface(ifaceArg CustomIface) CustomIface {
	return ifaceArg.SelfIface()
}

func (m *Test) TestSelfIfaceList(ctx context.Context, ifaceArg CustomIface) ([]CustomIface, error) {
	return ifaceArg.SelfIfaceList(ctx)
}

func (m *Test) TestOtherIface(ifaceArg CustomIface) OtherIface {
	return ifaceArg.OtherIface()
}

func (m *Test) TestOtherIfaceList(ctx context.Context, ifaceArg CustomIface) ([]OtherIface, error) {
	return ifaceArg.OtherIfaceList(ctx)
}

func (m *Test) TestIfaceListArgs(ctx context.Context, ifaces []CustomIface, otherIfaces []OtherIface) ([]string, error) {
	var strs []string
	for _, iface := range ifaces {
		str, err := iface.Str(ctx)
		if err != nil {
			return nil, fmt.Errorf("iface.Str: %w", err)
		}
		strs = append(strs, str)
	}
	for _, iface := range otherIfaces {
		str, err := iface.Foo(ctx)
		if err != nil {
			return nil, fmt.Errorf("iface.Foo: %w", err)
		}
		strs = append(strs, str)
	}
	return strs, nil
}

func (m *Test) WithIface(iface CustomIface) *Test {
	m.Iface = iface
	return m
}

func (m *Test) WithIfaceList(ifaces []CustomIface) *Test {
	m.IfaceList = ifaces
	return m
}

func (m *Test) WithOtherIfaceList(ifaces []OtherIface) *Test {
	m.OtherIfaceList = ifaces
	return m
}

func (m *Test) TestParentIfaceFields(ctx context.Context) ([]string, error) {
	var strs []string
	str, err := m.Iface.Str(ctx)
	if err != nil {
		return nil, fmt.Errorf("iface.Str: %w", err)
	}
	strs = append(strs, str)
	for _, iface := range m.IfaceList {
		str, err := iface.Str(ctx)
		if err != nil {
			return nil, fmt.Errorf("ifaceList.Str: %w", err)
		}
		strs = append(strs, str)
	}
	for _, iface := range m.OtherIfaceList {
		str, err := iface.Foo(ctx)
		if err != nil {
			return nil, fmt.Errorf("iface.Foo: %w", err)
		}
		strs = append(strs, str)
	}
	return strs, nil
}

type CustomObj struct {
	Iface        CustomIface
	IfaceList    []CustomIface
	Other        OtherCustomObj
	OtherPtr     *OtherCustomObj
	OtherList    []OtherCustomObj
	OtherPtrList []*OtherCustomObj
}

type OtherCustomObj struct {
	Iface     CustomIface
	IfaceList []CustomIface
}

func (m *Test) TestReturnCustomObj(ifaces []CustomIface, otherIfaces []OtherIface) *CustomObj {
	return &CustomObj{
		Iface:     ifaces[0],
		IfaceList: ifaces,
		Other: OtherCustomObj{
			Iface:     ifaces[0],
			IfaceList: ifaces,
		},
		OtherPtr: &OtherCustomObj{
			Iface:     ifaces[0],
			IfaceList: ifaces,
		},
		OtherList: []OtherCustomObj{
			{
				Iface:     ifaces[0],
				IfaceList: ifaces,
			},
		},
		OtherPtrList: []*OtherCustomObj{
			{
				Iface:     ifaces[0],
				IfaceList: ifaces,
			},
		},
	}
}
