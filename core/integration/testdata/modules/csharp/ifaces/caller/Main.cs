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
        await test.Void(impl.AsTestCustomIface());

        // String tests
        var str = await test.Str(impl.AsTestCustomIface());
        if (str != "a")
            throw new Exception($"Expected 'a', got '{str}'");

        var withStrResult = await test.WithStr(impl.AsTestCustomIface(), "c").Str();
        if (withStrResult != "c")
            throw new Exception($"Expected 'c', got '{withStrResult}'");

        // Optional string tests
        var withOptionalStr1 = await test.WithOptionalStr(impl.AsTestCustomIface(), "d").Str();
        if (withOptionalStr1 != "d")
            throw new Exception($"Expected 'd', got '{withOptionalStr1}'");

        var withOptionalStr2 = await test.WithOptionalStr(impl.AsTestCustomIface()).Str();
        if (withOptionalStr2 != "a")
            throw new Exception($"Expected 'a', got '{withOptionalStr2}'");

        // String list tests
        var strList = await test.StrList(impl.AsTestCustomIface());
        var strListArray = strList.ToList();
        if (strListArray.Count != 2 || strListArray[0] != "a" || strListArray[1] != "b")
            throw new Exception($"Expected ['a', 'b'], got [{string.Join(", ", strListArray)}]");

        // Int tests
        var intVal = await test.Int(impl.AsTestCustomIface());
        if (intVal != 1)
            throw new Exception($"Expected 1, got {intVal}");

        var withIntResult = await test.WithInt(impl.AsTestCustomIface(), 3).IntValue();
        if (withIntResult != 3)
            throw new Exception($"Expected 3, got {withIntResult}");

        // Bool tests
        var boolVal = await test.Bool(impl.AsTestCustomIface());
        if (!boolVal)
            throw new Exception($"Expected true, got {boolVal}");

        // Self interface tests
        var selfIface = test.SelfIface(impl.AsTestCustomIface());
        var selfStr = await selfIface.Str();
        if (selfStr != "aself")
            throw new Exception($"Expected 'aself', got '{selfStr}'");

        var selfIfaceList = await test.SelfIfaceList(impl.AsTestCustomIface());
        if (selfIfaceList.Count() != 2)
            throw new Exception($"Expected 2 self ifaces, got {selfIfaceList.Count()}");

        // Other interface tests
        var otherIface = test.OtherIface(impl.AsTestCustomIface());
        var otherStr = await otherIface.Foo();
        if (otherStr != "aother")
            throw new Exception($"Expected 'aother', got '{otherStr}'");

        var staticOtherList = await test.StaticOtherIfaceList(impl.AsTestCustomIface());
        var staticOtherArray = staticOtherList.ToList();
        if (staticOtherArray.Count != 2)
            throw new Exception($"Expected 2 static other ifaces, got {staticOtherArray.Count}");

        var other1Str = await staticOtherArray[0].Foo();
        if (other1Str != "aother1")
            throw new Exception($"Expected 'aother1', got '{other1Str}'");

        // Dynamic other interface list tests
        var dynamicEmpty = await test.DynamicOtherIfaceList(impl.AsTestCustomIface());
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
        var dynamicOthers = await test.DynamicOtherIfaceList(withOther2);
        var dynamicOthersArray = dynamicOthers.ToList();
        if (dynamicOthersArray.Count != 2)
            throw new Exception($"Expected 2 dynamic others, got {dynamicOthersArray.Count}");

        var dyn1 = await dynamicOthersArray[0].Foo();
        if (dyn1 != "arg1other")
            throw new Exception($"Expected 'arg1other', got '{dyn1}'");

        var dyn2 = await dynamicOthersArray[1].Foo();
        if (dyn2 != "arg2other")
            throw new Exception($"Expected 'arg2other', got '{dyn2}'");

        // Interface list args test
        var ifaceListArgs = await test.IfaceListArgs(
            [impl.AsTestCustomIface(), impl.SelfIface().AsTestCustomIface()],
            [impl.OtherIface().AsTestOtherIface(), impl.SelfIface().OtherIface().AsTestOtherIface()]
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

        // Interface-by-interface tests
        var dynamicByIfaceEmpty = await test.DynamicOtherIfaceByIfaceList(
            impl.AsTestCustomIface()
        );

        if (dynamicByIfaceEmpty.Count() != 0)
            throw new Exception(
                $"Expected 0 dynamic by iface others, got {dynamicByIfaceEmpty.Count()}"
            );

        var withByIface1 = test.WithOtherIfaceByIface(
            impl.AsTestCustomIface(),
            impl.WithStr("arg1").OtherIface().AsTestOtherIface()
        );

        var withByIface2 = test.WithOtherIfaceByIface(
            withByIface1,
            impl.WithStr("arg2").OtherIface().AsTestOtherIface()
        );

        var dynamicByIface = await test.DynamicOtherIfaceByIfaceList(withByIface2);
        var dynamicByIfaceArray = dynamicByIface.ToList();
        if (dynamicByIfaceArray.Count != 2)
            throw new Exception($"Expected 2 dynamic by iface, got {dynamicByIfaceArray.Count}");

        var byIface1 = await dynamicByIfaceArray[0].Foo();
        if (byIface1 != "arg1other")
            throw new Exception($"Expected 'arg1other', got '{byIface1}'");

        var byIface2 = await dynamicByIfaceArray[1].Foo();
        if (byIface2 != "arg2other")
            throw new Exception($"Expected 'arg2other', got '{byIface2}'");

        // Parent interface fields test with private field
        var privateImpl = Dag.Impl(
            ["private"],
            [99],
            [false],
            [Dag.Directory().WithNewFile("/private", "private")]
        );

        var parentFields = await test.WithIface(impl.AsTestCustomIface())
            .WithPrivateIface(privateImpl.AsTestCustomIface())
            .WithIfaceList([impl.AsTestCustomIface(), impl.SelfIface().AsTestCustomIface()])
            .WithOtherIfaceList([
                impl.OtherIface().AsTestOtherIface(),
                impl.SelfIface().OtherIface().AsTestOtherIface(),
            ])
            .ParentIfaceFields();

        var parentFieldsArray = parentFields.ToList();
        if (parentFieldsArray.Count != 6)
            throw new Exception($"Expected 6 fields, got {parentFieldsArray.Count}");
        if (
            parentFieldsArray[0] != "a"
            || parentFieldsArray[1] != "private"
            || parentFieldsArray[2] != "a"
            || parentFieldsArray[3] != "aself"
            || parentFieldsArray[4] != "aother"
            || parentFieldsArray[5] != "aselfother"
        )
            throw new Exception(
                $"Unexpected parentFields values: [{string.Join(", ", parentFieldsArray)}]"
            );

        // Optional interface test
        var withOptional1 = await test.WithOptionalIface()
            .WithOptionalIface(impl.AsTestCustomIface())
            .WithOptionalIface()
            .ParentIfaceFields();
        if (withOptional1.Count() != 1 || withOptional1[0] != "a")
            throw new Exception($"Expected ['a'], got [{string.Join(", ", withOptional1)}]");

        // Return custom object test with nested structure
        var customObj = test.ReturnCustomObj(
            [impl.AsTestCustomIface(), impl.SelfIface().AsTestCustomIface()],
            [impl.OtherIface().AsTestOtherIface(), impl.SelfIface().OtherIface().AsTestOtherIface()]
        );

        var customObjIface = customObj.Iface();
        if (customObjIface == null)
            throw new Exception("Expected Iface to be non-null");
        var customObjIfaceStr = await customObjIface.Str();
        if (customObjIfaceStr != "a")
            throw new Exception($"Expected 'a' from customObj.Iface, got '{customObjIfaceStr}'");

        var customObjIfaceList = await customObj.IfaceList();
        if (customObjIfaceList.Count() != 2)
            throw new Exception($"Expected 2 ifaces, got {customObjIfaceList.Count()}");
        if (await customObjIfaceList[0].Str() != "a")
            throw new Exception("Expected first iface to be 'a'");
        if (await customObjIfaceList[1].Str() != "aself")
            throw new Exception("Expected second iface to be 'aself'");

        var customObjOther = customObj.Other();
        if (customObjOther == null)
            throw new Exception("Expected Other to be non-null");
        var customObjOtherIface = customObjOther.Iface();
        if (customObjOtherIface == null)
            throw new Exception("Expected Other.Iface to be non-null");
        if (await customObjOtherIface.Str() != "a")
            throw new Exception("Expected Other.Iface to be 'a'");

        var customObjOtherList = await customObj.OtherList();
        if (customObjOtherList.Count() != 1)
            throw new Exception($"Expected 1 other, got {customObjOtherList.Count()}");
    }
}
