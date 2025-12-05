using System.Collections.Generic;
using System.Threading.Tasks;
using Dagger;

[Object]
public class Caller
{
    [Function]
    public async Task Test()
    {
        string[] strs = ["a", "b"];
        int[] ints = [1, 2];
        bool[] bools = [true, false];
        var dirs = new[]
        {
            Dag.Directory().WithNewFile("/file1", "file1"),
            Dag.Directory().WithNewFile("/file2", "file2"),
        };

        var impl = Dag.Impl(strs, ints, bools, dirs);
        var test = Dag.Test();

        // Void test - use AsTestCustomIface() conversion method
        await test.VoidAsync(impl.AsTestCustomIface());

        // String tests
        var str = await test.StrAsync(impl.AsTestCustomIface());
        if (str != "a")
            throw new Exception($"Expected 'a', got '{str}'");

        var withStrResult = await test.WithStr(impl.AsTestCustomIface(), "c").StrAsync();
        if (withStrResult != "c")
            throw new Exception($"Expected 'c', got '{withStrResult}'");

        // Optional string tests
        var withOptionalStr1 = await test.WithOptionalStr(impl.AsTestCustomIface(), "d").StrAsync();
        if (withOptionalStr1 != "d")
            throw new Exception($"Expected 'd', got '{withOptionalStr1}'");

        var withOptionalStr2 = await test.WithOptionalStr(impl.AsTestCustomIface()).StrAsync();
        if (withOptionalStr2 != "a")
            throw new Exception($"Expected 'a', got '{withOptionalStr2}'");

        // String list tests
        var strList = await test.StrListAsync(impl.AsTestCustomIface());
        var strListArray = strList.ToList();
        if (strListArray.Count != 2 || strListArray[0] != "a" || strListArray[1] != "b")
            throw new Exception($"Expected ['a', 'b'], got [{string.Join(", ", strListArray)}]");

        // Int tests
        var intVal = await test.IntAsync(impl.AsTestCustomIface());
        if (intVal != 1)
            throw new Exception($"Expected 1, got {intVal}");

        var withIntResult = await test.WithInt(impl.AsTestCustomIface(), 3).IntValueAsync();
        if (withIntResult != 3)
            throw new Exception($"Expected 3, got {withIntResult}");

        // Bool tests
        var boolVal = await test.BoolAsync(impl.AsTestCustomIface());
        if (!boolVal)
            throw new Exception($"Expected true, got {boolVal}");

        // Self interface tests
        var selfIface = test.SelfIface(impl.AsTestCustomIface());
        var selfStr = await selfIface.StrAsync();
        if (selfStr != "aself")
            throw new Exception($"Expected 'aself', got '{selfStr}'");

        var selfIfaceList = await test.SelfIfaceListAsync(impl.AsTestCustomIface());
        if (selfIfaceList.Count() != 2)
            throw new Exception($"Expected 2 self ifaces, got {selfIfaceList.Count()}");

        // Other interface tests
        var otherIface = test.OtherIface(impl.AsTestCustomIface());
        var otherStr = await otherIface.FooAsync();
        if (otherStr != "aother")
            throw new Exception($"Expected 'aother', got '{otherStr}'");

        var staticOtherList = await test.StaticOtherIfaceListAsync(impl.AsTestCustomIface());
        var staticOtherArray = staticOtherList.ToList();
        if (staticOtherArray.Count != 2)
            throw new Exception($"Expected 2 static other ifaces, got {staticOtherArray.Count}");
        var other1Str = await staticOtherArray[0].FooAsync();
        if (other1Str != "aother1")
            throw new Exception($"Expected 'aother1', got '{other1Str}'");

        // Dynamic other interface list tests
        var dynamicEmpty = await test.DynamicOtherIfaceListAsync(impl.AsTestCustomIface());
        if (dynamicEmpty.Count() != 0)
            throw new Exception($"Expected 0 dynamic others, got {dynamicEmpty.Count()}");

        var withOther1 = test.WithOtherIface(
            impl.AsTestCustomIface(),
            impl.WithStr("arg1").OtherIface().AsTestOtherIface()
        );
        var withOther2 = test.WithOtherIface(
            withOther1,
            impl.WithStr("arg2").OtherIface().AsTestOtherIface()
        );
        var dynamicOthers = await test.DynamicOtherIfaceListAsync(withOther2);
        var dynamicOthersArray = dynamicOthers.ToList();
        if (dynamicOthersArray.Count != 2)
            throw new Exception($"Expected 2 dynamic others, got {dynamicOthersArray.Count}");
        var dyn1 = await dynamicOthersArray[0].FooAsync();
        if (dyn1 != "arg1other")
            throw new Exception($"Expected 'arg1other', got '{dyn1}'");
        var dyn2 = await dynamicOthersArray[1].FooAsync();
        if (dyn2 != "arg2other")
            throw new Exception($"Expected 'arg2other', got '{dyn2}'");

        // Interface list args test
        var ifaceListArgs = await test.IfaceListArgsAsync(
            [
                impl.AsTestCustomIface(),
                impl.SelfIface().AsTestCustomIface(),
            ],
            [
                impl.OtherIface().AsTestOtherIface(),
                impl.SelfIface().OtherIface().AsTestOtherIface(),
            ]
        );
        if (ifaceListArgs.Count() != 4)
            throw new Exception($"Expected 4 items, got {ifaceListArgs.Count()}");
        if (
            ifaceListArgs[0] != "a"
            || ifaceListArgs[1] != "aself"
            || ifaceListArgs[2] != "aother"
            || ifaceListArgs[3] != "aselfother"
        )
            throw new Exception($"Unexpected ifaceListArgs values");

        // Parent interface fields test
        var parentFields = await test.WithIface(impl.AsTestCustomIface())
            .WithIfaceList(
                [
                    impl.AsTestCustomIface(),
                    impl.SelfIface().AsTestCustomIface(),
                ]
            )
            .WithOtherIfaceList(
                [
                    impl.OtherIface().AsTestOtherIface(),
                    impl.SelfIface().OtherIface().AsTestOtherIface(),
                ]
            )
            .ParentIfaceFieldsAsync();
        if (parentFields != "a a aself aother aselfother")
            throw new Exception($"Expected 'a a aself aother aselfother', got '{parentFields}'");

        // Return custom object test
        var customObj = test.ReturnCustomObj(
            [
                impl.AsTestCustomIface(),
                impl.SelfIface().AsTestCustomIface(),
            ],
            [
                impl.OtherIface().AsTestOtherIface(),
                impl.SelfIface().OtherIface().AsTestOtherIface(),
            ]
        );

        if (await customObj.FooAsync() != "foo")
            throw new Exception($"Expected 'foo', got '{await customObj.FooAsync()}'");

        var customObjSelfIface = customObj.SelfIface();
        if (customObjSelfIface == null)
            throw new Exception("Expected SelfIface to be non-null");

        var customObjSelfStr = await customObjSelfIface.StrAsync();
        if (customObjSelfStr != "a")
            throw new Exception($"Expected 'a' from customObj.SelfIface, got '{customObjSelfStr}'");

        var customObjOtherIface = customObj.OtherIface();
        if (customObjOtherIface == null)
            throw new Exception("Expected OtherIface to be non-null");

        var customObjOtherStr = await customObjOtherIface.FooAsync();
        if (customObjOtherStr != "aother")
            throw new Exception($"Expected 'aother' from customObj.OtherIface, got '{customObjOtherStr}'");

    }
}
