package main

import (
	"context"
	"fmt"
)

type Test struct {
	IfaceField CustomIface

	IfaceFieldNeverSet CustomIface

	// +private
	IfacePrivateField CustomIface

	IfaceListField      []CustomIface
	OtherIfaceListField []OtherIface
}

type CustomIface interface {
	DaggerObject
	Void(ctx context.Context) error

	Str(ctx context.Context) (string, error)
	WithStr(ctx context.Context, strArg string) CustomIface
	WithOptionalTypeStr(ctx context.Context, strArg Optional[string]) CustomIface
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

func (m *Test) Void(ctx context.Context, ifaceArg CustomIface) error {
	return ifaceArg.Void(ctx)
}

func (m *Test) Str(ctx context.Context, ifaceArg CustomIface) (string, error) {
	return ifaceArg.Str(ctx)
}

func (m *Test) WithStr(ctx context.Context, ifaceArg CustomIface, strArg string) CustomIface {
	return ifaceArg.WithStr(ctx, strArg)
}

func (m *Test) WithOptionalTypeStr(ctx context.Context, ifaceArg CustomIface, strArg Optional[string]) CustomIface {
	return ifaceArg.WithOptionalTypeStr(ctx, strArg)
}

func (m *Test) WithOptionalPragmaStr(
	ctx context.Context,
	ifaceArg CustomIface,
	// +optional
	strArg string,
) CustomIface {
	return ifaceArg.WithOptionalPragmaStr(ctx, strArg)
}

func (m *Test) StrList(ctx context.Context, ifaceArg CustomIface) ([]string, error) {
	return ifaceArg.StrList(ctx)
}

func (m *Test) WithStrList(ctx context.Context, ifaceArg CustomIface, strList []string) CustomIface {
	return ifaceArg.WithStrList(ctx, strList)
}

func (m *Test) Int(ctx context.Context, ifaceArg CustomIface) (int, error) {
	return ifaceArg.Int(ctx)
}

func (m *Test) WithInt(ctx context.Context, ifaceArg CustomIface, intArg int) CustomIface {
	return ifaceArg.WithInt(ctx, intArg)
}

func (m *Test) IntList(ctx context.Context, ifaceArg CustomIface) ([]int, error) {
	return ifaceArg.IntList(ctx)
}

func (m *Test) WithIntList(ctx context.Context, ifaceArg CustomIface, intList []int) CustomIface {
	return ifaceArg.WithIntList(ctx, intList)
}

func (m *Test) Bool(ctx context.Context, ifaceArg CustomIface) (bool, error) {
	return ifaceArg.Bool(ctx)
}

func (m *Test) WithBool(ctx context.Context, ifaceArg CustomIface, boolArg bool) CustomIface {
	return ifaceArg.WithBool(ctx, boolArg)
}

func (m *Test) BoolList(ctx context.Context, ifaceArg CustomIface) ([]bool, error) {
	return ifaceArg.BoolList(ctx)
}

func (m *Test) WithBoolList(ctx context.Context, ifaceArg CustomIface, boolList []bool) CustomIface {
	return ifaceArg.WithBoolList(ctx, boolList)
}

func (m *Test) Obj(ifaceArg CustomIface) *Directory {
	return ifaceArg.Obj()
}

func (m *Test) WithObj(ifaceArg CustomIface, objArg *Directory) CustomIface {
	return ifaceArg.WithObj(objArg)
}

func (m *Test) ObjList(ctx context.Context, ifaceArg CustomIface) ([]*Directory, error) {
	return ifaceArg.ObjList(ctx)
}

func (m *Test) WithObjList(ctx context.Context, ifaceArg CustomIface, objList []*Directory) CustomIface {
	return ifaceArg.WithObjList(ctx, objList)
}

func (m *Test) SelfIface(ifaceArg CustomIface) CustomIface {
	return ifaceArg.SelfIface()
}

func (m *Test) SelfIfaceList(ctx context.Context, ifaceArg CustomIface) ([]CustomIface, error) {
	return ifaceArg.SelfIfaceList(ctx)
}

func (m *Test) OtherIface(ifaceArg CustomIface) OtherIface {
	return ifaceArg.OtherIface()
}

func (m *Test) OtherIfaceList(ctx context.Context, ifaceArg CustomIface) ([]OtherIface, error) {
	return ifaceArg.OtherIfaceList(ctx)
}

func (m *Test) IfaceListArgs(ctx context.Context, ifaces []CustomIface, otherIfaces []OtherIface) ([]string, error) {
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
	m.IfaceField = iface
	return m
}

func (m *Test) WithOptionalTypeIface(iface Optional[CustomIface]) *Test {
	if iface, ok := iface.Get(); ok {
		m.IfaceField = iface
	}
	return m
}

func (m *Test) WithOptionalPragmaIface(
	// +optional
	iface CustomIface,
) *Test {
	if iface != nil {
		m.IfaceField = iface
	}
	return m
}

func (m *Test) WithIfaceList(ifaces []CustomIface) *Test {
	m.IfaceListField = ifaces
	return m
}

func (m *Test) WithOtherIfaceList(ifaces []OtherIface) *Test {
	m.OtherIfaceListField = ifaces
	return m
}

func (m *Test) WithPrivateIface(iface CustomIface) *Test {
	m.IfacePrivateField = iface
	return m
}

func (m *Test) ParentIfaceFields(ctx context.Context) ([]string, error) {
	var strs []string
	if m.IfaceField != nil {
		str, err := m.IfaceField.Str(ctx)
		if err != nil {
			return nil, fmt.Errorf("iface.Str: %w", err)
		}
		strs = append(strs, str)
	}
	if m.IfacePrivateField != nil {
		str, err := m.IfacePrivateField.Str(ctx)
		if err != nil {
			return nil, fmt.Errorf("ifacePrivateField.Str: %w", err)
		}
		strs = append(strs, str)
	}
	for _, iface := range m.IfaceListField {
		str, err := iface.Str(ctx)
		if err != nil {
			return nil, fmt.Errorf("ifaceList.Str: %w", err)
		}
		strs = append(strs, str)
	}
	for _, iface := range m.OtherIfaceListField {
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

func (m *Test) ReturnCustomObj(ifaces []CustomIface, otherIfaces []OtherIface) *CustomObj {
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
