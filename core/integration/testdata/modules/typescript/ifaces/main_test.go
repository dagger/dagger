package main

import (
	"context"
	"dagger/caller/internal/dagger"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIface(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	strs := []string{"a", "b"}
	ints := []int{1, 2}
	bools := []bool{true, false}
	dirs := []*dagger.Directory{
		dag.Directory().WithNewFile("/file1", "file1"),
		dag.Directory().WithNewFile("/file2", "file2"),
	}
	impl := dag.Impl(strs, ints, bools, dirs)

	test := dag.Test()

	t.Run("void", func(t *testing.T) {
		t.Parallel()
		err := test.Void(ctx, impl.AsTestCustomIface())
		require.NoError(t, err)
	})

	t.Run("str", func(t *testing.T) {
		t.Parallel()
		str, err := test.Str(ctx, impl.AsTestCustomIface())
		require.NoError(t, err)
		require.Equal(t, "a", str)
	})
	t.Run("withStr", func(t *testing.T) {
		t.Parallel()
		str, err := test.WithStr(impl.AsTestCustomIface(), "c").Str(ctx)
		require.NoError(t, err)
		require.Equal(t, "c", str)
	})
	t.Run("withOptionalStr", func(t *testing.T) {
		t.Parallel()
		str, err := test.WithOptionalStr(impl.AsTestCustomIface(), dagger.TestWithOptionalStrOpts{
			StrArg: "d",
		}).Str(ctx)
		require.NoError(t, err)
		require.Equal(t, "d", str)
		str, err = test.WithOptionalStr(impl.AsTestCustomIface()).Str(ctx)
		require.NoError(t, err)
		require.Equal(t, "a", str)
	})
	t.Run("strList", func(t *testing.T) {
		t.Parallel()
		strs, err := test.StrList(ctx, impl.AsTestCustomIface())
		require.NoError(t, err)
		require.Equal(t, []string{"a", "b"}, strs)
	})
	t.Run("withStrList", func(t *testing.T) {
		t.Parallel()
		strs, err := test.WithStrList(impl.AsTestCustomIface(), []string{"c", "d"}).StrList(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"c", "d"}, strs)
	})

	t.Run("int", func(t *testing.T) {
		t.Parallel()
		i, err := test.Int(ctx, impl.AsTestCustomIface())
		require.NoError(t, err)
		require.Equal(t, 1, i)
	})
	t.Run("withInt", func(t *testing.T) {
		t.Parallel()
		i, err := test.WithInt(impl.AsTestCustomIface(), 3).Int(ctx)
		require.NoError(t, err)
		require.Equal(t, 3, i)
	})
	t.Run("intList", func(t *testing.T) {
		t.Parallel()
		ints, err := test.IntList(ctx, impl.AsTestCustomIface())
		require.NoError(t, err)
		require.Equal(t, []int{1, 2}, ints)
	})
	t.Run("withIntList", func(t *testing.T) {
		t.Parallel()
		ints, err := test.WithIntList(impl.AsTestCustomIface(), []int{3, 4}).IntList(ctx)
		require.NoError(t, err)
		require.Equal(t, []int{3, 4}, ints)
	})

	t.Run("bool", func(t *testing.T) {
		t.Parallel()
		b, err := test.Bool(ctx, impl.AsTestCustomIface())
		require.NoError(t, err)
		require.Equal(t, true, b)
	})
	t.Run("withBool", func(t *testing.T) {
		t.Parallel()
		b, err := test.WithBool(impl.AsTestCustomIface(), false).Bool(ctx)
		require.NoError(t, err)
		require.Equal(t, false, b)
	})
	t.Run("boolList", func(t *testing.T) {
		t.Parallel()
		bools, err := test.BoolList(ctx, impl.AsTestCustomIface())
		require.NoError(t, err)
		require.Equal(t, []bool{true, false}, bools)
	})
	t.Run("withBoolList", func(t *testing.T) {
		t.Parallel()
		bools, err := test.WithBoolList(impl.AsTestCustomIface(), []bool{false, true}).BoolList(ctx)
		require.NoError(t, err)
		require.Equal(t, []bool{false, true}, bools)
	})

	t.Run("withMany", func(t *testing.T) {
		t.Parallel()
		iface := test.
			WithStr(impl.AsTestCustomIface(), "c").
			WithInt(3).
			WithBool(true)

		str, err := iface.Str(ctx)
		require.NoError(t, err)
		require.Equal(t, "c", str)

		i, err := iface.Int(ctx)
		require.NoError(t, err)
		require.Equal(t, 3, i)

		b, err := iface.Bool(ctx)
		require.NoError(t, err)
		require.Equal(t, true, b)
	})

	t.Run("obj", func(t *testing.T) {
		t.Parallel()
		dir := test.Obj(impl.AsTestCustomIface())
		dirEnts, err := dir.Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, dirEnts, "file1")
	})

	t.Run("withObj", func(t *testing.T) {
		t.Parallel()
		dir := test.WithObj(impl.AsTestCustomIface(), dirs[1]).Obj()
		dirEnts, err := dir.Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, dirEnts, "file2")
	})

	t.Run("objList", func(t *testing.T) {
		t.Parallel()
		dirs, err := test.ObjList(ctx, impl.AsTestCustomIface())
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
		dirs, err := test.WithObjList(impl.AsTestCustomIface(), []*dagger.Directory{
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
		iface := test.SelfIface(impl.AsTestCustomIface())
		str, err := iface.Str(ctx)
		require.NoError(t, err)
		require.Equal(t, "aself", str)
	})
	t.Run("selfIfaceList", func(t *testing.T) {
		t.Parallel()
		ifaces, err := test.SelfIfaceList(ctx, impl.AsTestCustomIface())
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
		iface := test.OtherIface(impl.AsTestCustomIface())
		str, err := iface.Foo(ctx)
		require.NoError(t, err)
		require.Equal(t, "aother", str)
	})
	t.Run("staticOtherIfaceList", func(t *testing.T) {
		t.Parallel()
		ifaces, err := test.StaticOtherIfaceList(ctx, impl.AsTestCustomIface())
		require.NoError(t, err)
		require.Len(t, ifaces, 2)
		str1, err := ifaces[0].Foo(ctx)
		require.NoError(t, err)
		require.Equal(t, "aother1", str1)
		str2, err := ifaces[1].Foo(ctx)
		require.NoError(t, err)
		require.Equal(t, "aother2", str2)
	})

	t.Run("dynamicOtherIfaceList", func(t *testing.T) {
		t.Parallel()
		ifaces, err := test.DynamicOtherIfaceList(ctx, impl.AsTestCustomIface())
		require.NoError(t, err)
		require.Len(t, ifaces, 0)
		ifaces, err = test.DynamicOtherIfaceList(ctx,
			test.WithOtherIface(
				test.WithOtherIface(
					impl.AsTestCustomIface(),
					impl.WithStr("arg1").OtherIface().AsTestOtherIface(),
				),
				impl.WithStr("arg2").OtherIface().AsTestOtherIface(),
			),
		)
		require.NoError(t, err)
		require.Len(t, ifaces, 2)
		str1, err := ifaces[0].Foo(ctx)
		require.NoError(t, err)
		require.Equal(t, "arg1other", str1)
		str2, err := ifaces[1].Foo(ctx)
		require.NoError(t, err)
		require.Equal(t, "arg2other", str2)
	})

	t.Run("dynamicOtherIfaceByIfaceList", func(t *testing.T) {
		t.Parallel()
		ifaces, err := test.DynamicOtherIfaceByIfaceList(ctx, impl.AsTestCustomIface())
		require.NoError(t, err)
		require.Len(t, ifaces, 0)
		ifaces, err = test.DynamicOtherIfaceByIfaceList(ctx,
			test.WithOtherIfaceByIface(
				test.WithOtherIfaceByIface(
					impl.AsTestCustomIface(),
					impl.WithStr("arg1").OtherIface().AsTestOtherIface(),
				),
				impl.WithStr("arg2").OtherIface().AsTestOtherIface(),
			),
		)
		require.NoError(t, err)
		require.Len(t, ifaces, 2)
		str1, err := ifaces[0].Foo(ctx)
		require.NoError(t, err)
		require.Equal(t, "arg1other", str1)
		str2, err := ifaces[1].Foo(ctx)
		require.NoError(t, err)
		require.Equal(t, "arg2other", str2)
	})

	t.Run("ifaceListArgs", func(t *testing.T) {
		t.Parallel()
		strs, err := test.IfaceListArgs(ctx,
			[]*dagger.TestCustomIface{
				impl.AsTestCustomIface(),
				impl.SelfIface().AsTestCustomIface(),
			},
			[]*dagger.TestOtherIface{
				impl.OtherIface().AsTestOtherIface(),
				impl.SelfIface().OtherIface().AsTestOtherIface(),
			},
		)
		require.NoError(t, err)
		require.Equal(t, []string{"a", "aself", "aother", "aselfother"}, strs)
	})

	t.Run("parentIfaceFields", func(t *testing.T) {
		t.Parallel()
		t.Run("basic", func(t *testing.T) {
			t.Parallel()
			strs, err := test.
				WithIface(impl.AsTestCustomIface()).
				WithPrivateIface(dag.Impl([]string{"private"}, []int{99}, []bool{false}, []*dagger.Directory{dag.Directory()}).AsTestCustomIface()).
				WithIfaceList([]*dagger.TestCustomIface{
					impl.AsTestCustomIface(),
					impl.SelfIface().AsTestCustomIface(),
				}).
				WithOtherIfaceList([]*dagger.TestOtherIface{
					impl.OtherIface().AsTestOtherIface(),
					impl.SelfIface().OtherIface().AsTestOtherIface(),
				}).
				ParentIfaceFields(ctx)
			require.NoError(t, err)
			require.Equal(t, []string{"a", "private", "a", "aself", "aother", "aselfother"}, strs)
		})
		t.Run("optionals", func(t *testing.T) {
			t.Parallel()
			strs, err := test.
				WithOptionalIface().
				WithOptionalIface(dagger.TestWithOptionalIfaceOpts{Iface: impl.AsTestCustomIface()}).
				WithOptionalIface().
				ParentIfaceFields(ctx)
			require.NoError(t, err)
			require.Equal(t, []string{"a"}, strs)
		})
	})

	t.Run("returnCustomObj", func(t *testing.T) {
		t.Parallel()
		customObj := test.ReturnCustomObj(
			[]*dagger.TestCustomIface{
				impl.AsTestCustomIface(),
				impl.SelfIface().AsTestCustomIface(),
			},
			[]*dagger.TestOtherIface{
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
	})
}
