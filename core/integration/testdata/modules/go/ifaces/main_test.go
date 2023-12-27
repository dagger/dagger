package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIface(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	strs := []string{"a", "b"}
	ints := []int{1, 2}
	bools := []bool{true, false}
	dirs := []*Directory{
		dag.Directory().WithNewFile("/file1", "file1"),
		dag.Directory().WithNewFile("/file2", "file2"),
	}
	impl := dag.Impl(strs, ints, bools, dirs)
	// otherImpl := impl.OtherIface()

	test := dag.Test()

	t.Run("void", func(t *testing.T) {
		t.Parallel()
		_, err := test.TestVoid(ctx, impl.AsTestCustomIface())
		require.NoError(t, err)
	})

	t.Run("str", func(t *testing.T) {
		t.Parallel()
		str, err := test.TestStr(ctx, impl.AsTestCustomIface())
		require.NoError(t, err)
		require.Equal(t, "a", str)
	})
	t.Run("withStr", func(t *testing.T) {
		t.Parallel()
		str, err := test.TestWithStr(impl.AsTestCustomIface(), "c").Str(ctx)
		require.NoError(t, err)
		require.Equal(t, "c", str)
	})
	t.Run("strList", func(t *testing.T) {
		t.Parallel()
		strs, err := test.TestStrList(ctx, impl.AsTestCustomIface())
		require.NoError(t, err)
		require.Equal(t, []string{"a", "b"}, strs)
	})
	t.Run("withStrList", func(t *testing.T) {
		t.Parallel()
		strs, err := test.TestWithStrList(impl.AsTestCustomIface(), []string{"c", "d"}).StrList(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"c", "d"}, strs)
	})

	t.Run("int", func(t *testing.T) {
		t.Parallel()
		i, err := test.TestInt(ctx, impl.AsTestCustomIface())
		require.NoError(t, err)
		require.Equal(t, 1, i)
	})
	t.Run("withInt", func(t *testing.T) {
		t.Parallel()
		i, err := test.TestWithInt(impl.AsTestCustomIface(), 3).Int(ctx)
		require.NoError(t, err)
		require.Equal(t, 3, i)
	})
	t.Run("intList", func(t *testing.T) {
		t.Parallel()
		ints, err := test.TestIntList(ctx, impl.AsTestCustomIface())
		require.NoError(t, err)
		require.Equal(t, []int{1, 2}, ints)
	})
	t.Run("withIntList", func(t *testing.T) {
		t.Parallel()
		ints, err := test.TestWithIntList(impl.AsTestCustomIface(), []int{3, 4}).IntList(ctx)
		require.NoError(t, err)
		require.Equal(t, []int{3, 4}, ints)
	})

	t.Run("bool", func(t *testing.T) {
		t.Parallel()
		b, err := test.TestBool(ctx, impl.AsTestCustomIface())
		require.NoError(t, err)
		require.Equal(t, true, b)
	})
	t.Run("withBool", func(t *testing.T) {
		t.Parallel()
		b, err := test.TestWithBool(impl.AsTestCustomIface(), false).Bool(ctx)
		require.NoError(t, err)
		require.Equal(t, false, b)
	})
	t.Run("boolList", func(t *testing.T) {
		t.Parallel()
		bools, err := test.TestBoolList(ctx, impl.AsTestCustomIface())
		require.NoError(t, err)
		require.Equal(t, []bool{true, false}, bools)
	})
	t.Run("withBoolList", func(t *testing.T) {
		t.Parallel()
		bools, err := test.TestWithBoolList(impl.AsTestCustomIface(), []bool{false, true}).BoolList(ctx)
		require.NoError(t, err)
		require.Equal(t, []bool{false, true}, bools)
	})

	t.Run("obj", func(t *testing.T) {
		t.Parallel()
		dir := test.TestObj(impl.AsTestCustomIface())
		dirEnts, err := dir.Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, dirEnts, "file1")
	})
	t.Run("withObj", func(t *testing.T) {
		t.Parallel()
		dir := test.TestWithObj(impl.AsTestCustomIface(), dirs[1]).Obj()
		dirEnts, err := dir.Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, dirEnts, "file2")
	})
	t.Run("objList", func(t *testing.T) {
		t.Parallel()
		dirs, err := test.TestObjList(ctx, impl.AsTestCustomIface())
		require.NoError(t, err)
		require.Len(t, dirs, 2)
		dirEnts1, err := dirs[0].Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, dirEnts1, "file1")
		dirEnts2, err := dirs[1].Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, dirEnts2, "file2")
	})
	t.Run("withObjList", func(t *testing.T) {
		t.Parallel()
		dirs, err := test.TestWithObjList(impl.AsTestCustomIface(), []*Directory{
			dag.Directory().WithNewFile("/file3", "file3"),
			dag.Directory().WithNewFile("/file4", "file4"),
		}).ObjList(ctx)
		require.NoError(t, err)
		require.Len(t, dirs, 2)
		dirEnts1, err := dirs[0].Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, dirEnts1, "file3")
		dirEnts2, err := dirs[1].Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, dirEnts2, "file4")
	})

	t.Run("selfIface", func(t *testing.T) {
		t.Parallel()
		iface := test.TestSelfIface(impl.AsTestCustomIface())
		str, err := iface.Str(ctx)
		require.NoError(t, err)
		require.Equal(t, "aself", str)
	})
	t.Run("selfIfaceList", func(t *testing.T) {
		t.Parallel()
		ifaces, err := test.TestSelfIfaceList(ctx, impl.AsTestCustomIface())
		require.NoError(t, err)
		require.Len(t, ifaces, 2)
		str1, err := ifaces[0].Str(ctx)
		require.NoError(t, err)
		require.Equal(t, "aself1", str1)
		str2, err := ifaces[1].Str(ctx)
		require.NoError(t, err)
		require.Equal(t, "aself2", str2)
	})

	t.Run("otherIface", func(t *testing.T) {
		t.Parallel()
		iface := test.TestOtherIface(impl.AsTestCustomIface())
		str, err := iface.Foo(ctx)
		require.NoError(t, err)
		require.Equal(t, "aother", str)
	})
	t.Run("otherIfaceList", func(t *testing.T) {
		t.Parallel()
		ifaces, err := test.TestOtherIfaceList(ctx, impl.AsTestCustomIface())
		require.NoError(t, err)
		require.Len(t, ifaces, 2)
		str1, err := ifaces[0].Foo(ctx)
		require.NoError(t, err)
		require.Equal(t, "aother1", str1)
		str2, err := ifaces[1].Foo(ctx)
		require.NoError(t, err)
		require.Equal(t, "aother2", str2)
	})

	t.Run("ifaceListArgs", func(t *testing.T) {
		t.Parallel()
		strs, err := test.TestIfaceListArgs(ctx,
			[]*TestCustomIface{
				impl.AsTestCustomIface(),
				impl.SelfIface().AsTestCustomIface(),
			},
			[]*TestOtherIface{
				impl.OtherIface().AsTestOtherIface(),
				impl.SelfIface().OtherIface().AsTestOtherIface(),
			},
		)
		require.NoError(t, err)
		require.Equal(t, []string{"a", "aself", "aother", "aselfother"}, strs)
	})

	t.Run("parentIfaceFields", func(t *testing.T) {
		t.Parallel()
		strs, err := test.
			WithIface(impl.AsTestCustomIface()).
			WithIfaceList([]*TestCustomIface{
				impl.AsTestCustomIface(),
				impl.SelfIface().AsTestCustomIface(),
			}).
			WithOtherIfaceList([]*TestOtherIface{
				impl.OtherIface().AsTestOtherIface(),
				impl.SelfIface().OtherIface().AsTestOtherIface(),
			}).
			TestParentIfaceFields(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"a", "a", "aself", "aother", "aselfother"}, strs)
	})

	t.Run("returnCustomObj", func(t *testing.T) {
		t.Parallel()
		customObj := test.TestReturnCustomObj(
			[]*TestCustomIface{
				impl.AsTestCustomIface(),
				impl.SelfIface().AsTestCustomIface(),
			},
			[]*TestOtherIface{
				impl.OtherIface().AsTestOtherIface(),
				impl.SelfIface().OtherIface().AsTestOtherIface(),
			},
		)

		ifaceStr, err := customObj.Iface().Str(ctx)
		require.NoError(t, err)
		require.Equal(t, "a", ifaceStr)

		ifaces, err := customObj.IfaceList(ctx)
		require.NoError(t, err)
		require.Len(t, ifaces, 2)
		ifaceStr1, err := ifaces[0].Str(ctx)
		require.NoError(t, err)
		require.Equal(t, "a", ifaceStr1)
		ifaceStr2, err := ifaces[1].Str(ctx)
		require.NoError(t, err)
		require.Equal(t, "aself", ifaceStr2)

		otherCustomObjIfaceStr, err := customObj.Other().Iface().Str(ctx)
		require.NoError(t, err)

		require.Equal(t, "a", otherCustomObjIfaceStr)
		otherCustomObjIfaces, err := customObj.Other().IfaceList(ctx)
		require.NoError(t, err)
		require.Len(t, otherCustomObjIfaces, 2)
		otherCustomObjIfaceStr1, err := otherCustomObjIfaces[0].Str(ctx)
		require.NoError(t, err)
		require.Equal(t, "a", otherCustomObjIfaceStr1)
		otherCustomObjIfaceStr2, err := otherCustomObjIfaces[1].Str(ctx)
		require.NoError(t, err)
		require.Equal(t, "aself", otherCustomObjIfaceStr2)

		otherPtrCustomObjIfaceStr, err := customObj.OtherPtr().Iface().Str(ctx)
		require.NoError(t, err)
		require.Equal(t, "a", otherPtrCustomObjIfaceStr)

		otherPtrCustomObjIfaces, err := customObj.OtherPtr().IfaceList(ctx)
		require.NoError(t, err)
		require.Len(t, otherPtrCustomObjIfaces, 2)
		otherPtrCustomObjIfaceStr1, err := otherPtrCustomObjIfaces[0].Str(ctx)
		require.NoError(t, err)
		require.Equal(t, "a", otherPtrCustomObjIfaceStr1)
		otherPtrCustomObjIfaceStr2, err := otherPtrCustomObjIfaces[1].Str(ctx)
		require.NoError(t, err)
		require.Equal(t, "aself", otherPtrCustomObjIfaceStr2)

		otherCustomObjList, err := customObj.OtherList(ctx)
		require.NoError(t, err)
		require.Len(t, otherCustomObjList, 1)
		otherCustomObjListStr, err := otherCustomObjList[0].Iface().Str(ctx)
		require.NoError(t, err)
		require.Equal(t, "a", otherCustomObjListStr)
		otherCustomObjListIfaces, err := otherCustomObjList[0].IfaceList(ctx)
		require.NoError(t, err)
		require.Len(t, otherCustomObjListIfaces, 2)
		otherCustomObjListIfaceStr1, err := otherCustomObjListIfaces[0].Str(ctx)
		require.NoError(t, err)
		require.Equal(t, "a", otherCustomObjListIfaceStr1)
		otherCustomObjListIfaceStr2, err := otherCustomObjListIfaces[1].Str(ctx)
		require.NoError(t, err)
		require.Equal(t, "aself", otherCustomObjListIfaceStr2)

		otherCustomObjPtrList, err := customObj.OtherPtrList(ctx)
		require.NoError(t, err)
		require.Len(t, otherCustomObjPtrList, 1)
		otherCustomObjPtrListStr, err := otherCustomObjPtrList[0].Iface().Str(ctx)
		require.NoError(t, err)
		require.Equal(t, "a", otherCustomObjPtrListStr)
		otherCustomObjPtrListIfaces, err := otherCustomObjPtrList[0].IfaceList(ctx)
		require.NoError(t, err)
		require.Len(t, otherCustomObjPtrListIfaces, 2)
		otherCustomObjPtrListIfaceStr1, err := otherCustomObjPtrListIfaces[0].Str(ctx)
		require.NoError(t, err)
		require.Equal(t, "a", otherCustomObjPtrListIfaceStr1)
		otherCustomObjPtrListIfaceStr2, err := otherCustomObjPtrListIfaces[1].Str(ctx)
		require.NoError(t, err)
		require.Equal(t, "aself", otherCustomObjPtrListIfaceStr2)
	})
}
