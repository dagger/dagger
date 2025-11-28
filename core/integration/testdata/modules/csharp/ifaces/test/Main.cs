using System.Collections.Generic;
using System.Threading.Tasks;
using Dagger;

[Interface]
public interface CustomIface
{
    [Function]
    Task Void();

    [Function]
    Task<string> Str();

    [Function]
    CustomIface WithStr(string strArg);

    [Function]
    Task<List<string>> StrList();

    [Function]
    CustomIface WithStrList(List<string> strListArg);

    [Function]
    Task<int> IntValue();

    [Function]
    CustomIface WithInt(int intArg);

    [Function]
    Task<List<int>> IntList();

    [Function]
    CustomIface WithIntList(List<int> intListArg);

    [Function]
    Task<bool> BoolValue();

    [Function]
    CustomIface WithBool(bool boolArg);

    [Function]
    Task<List<bool>> BoolList();

    [Function]
    CustomIface WithBoolList(List<bool> boolListArg);

    [Function]
    Directory Obj();

    [Function]
    CustomIface WithObj(Directory objArg);

    [Function]
    Task<List<Directory>> ObjList();

    [Function]
    CustomIface WithObjList(List<Directory> objListArg);

    [Function]
    CustomIface SelfIface();

    [Function]
    Task<List<CustomIface>> SelfIfaceList();

    [Function]
    OtherIface OtherIface();

    [Function]
    Task<List<OtherIface>> StaticOtherIfaceList();

    [Function]
    CustomIface WithOptionalStr(string? strArg = null);

    [Function]
    CustomIface WithOptionalObj(Directory? objArg = null);

    [Function]
    CustomIface WithOtherIface(OtherIface other);

    [Function]
    Task<List<OtherIface>> DynamicOtherIfaceList();
}

[Interface]
public interface OtherIface
{
    [Function]
    Task<string> Foo();
}

[Object]
public class Test
{
    [Field]
    public CustomIface? IfaceField { get; set; }

    [Field]
    public List<CustomIface> IfaceListField { get; set; } = new List<CustomIface>();

    [Field]
    public List<OtherIface> OtherIfaceListField { get; set; } = new List<OtherIface>();

    [Function]
    public async Task Void(CustomIface ifaceArg)
    {
        await ifaceArg.Void();
    }

    [Function]
    public async Task<string> Str(CustomIface ifaceArg)
    {
        return await ifaceArg.Str();
    }

    [Function]
    public CustomIface WithStr(CustomIface ifaceArg, string strArg)
    {
        return ifaceArg.WithStr(strArg);
    }

    [Function]
    public async Task<List<string>> StrList(CustomIface ifaceArg)
    {
        return await ifaceArg.StrList();
    }

    [Function]
    public CustomIface WithStrList(CustomIface ifaceArg, List<string> strList)
    {
        return ifaceArg.WithStrList(strList);
    }

    [Function]
    public async Task<int> Int(CustomIface ifaceArg)
    {
        return await ifaceArg.IntValue();
    }

    [Function]
    public CustomIface WithInt(CustomIface ifaceArg, int intArg)
    {
        return ifaceArg.WithInt(intArg);
    }

    [Function]
    public async Task<List<int>> IntList(CustomIface ifaceArg)
    {
        return await ifaceArg.IntList();
    }

    [Function]
    public CustomIface WithIntList(CustomIface ifaceArg, List<int> intList)
    {
        return ifaceArg.WithIntList(intList);
    }

    [Function]
    public async Task<bool> Bool(CustomIface ifaceArg)
    {
        return await ifaceArg.BoolValue();
    }

    [Function]
    public CustomIface WithBool(CustomIface ifaceArg, bool boolArg)
    {
        return ifaceArg.WithBool(boolArg);
    }

    [Function]
    public async Task<List<bool>> BoolList(CustomIface ifaceArg)
    {
        return await ifaceArg.BoolList();
    }

    [Function]
    public CustomIface WithBoolList(CustomIface ifaceArg, List<bool> boolList)
    {
        return ifaceArg.WithBoolList(boolList);
    }

    [Function]
    public Directory Obj(CustomIface ifaceArg)
    {
        return ifaceArg.Obj();
    }

    [Function]
    public CustomIface WithObj(CustomIface ifaceArg, Directory objArg)
    {
        return ifaceArg.WithObj(objArg);
    }

    [Function]
    public async Task<List<Directory>> ObjList(CustomIface ifaceArg)
    {
        return await ifaceArg.ObjList();
    }

    [Function]
    public CustomIface WithObjList(CustomIface ifaceArg, List<Directory> objList)
    {
        return ifaceArg.WithObjList(objList);
    }

    [Function]
    public CustomIface SelfIface(CustomIface ifaceArg)
    {
        return ifaceArg.SelfIface();
    }

    [Function]
    public async Task<List<CustomIface>> SelfIfaceList(CustomIface ifaceArg)
    {
        return await ifaceArg.SelfIfaceList();
    }

    [Function]
    public OtherIface OtherIface(CustomIface ifaceArg)
    {
        return ifaceArg.OtherIface();
    }

    [Function]
    public async Task<List<OtherIface>> StaticOtherIfaceList(CustomIface ifaceArg)
    {
        return await ifaceArg.StaticOtherIfaceList();
    }

    [Function]
    public async Task<List<string>> IfaceListArgs(
        List<CustomIface> ifaces,
        List<OtherIface> otherIfaces
    )
    {
        var strs = new List<string>();
        foreach (var iface in ifaces)
        {
            strs.Add(await iface.Str());
        }
        foreach (var iface in otherIfaces)
        {
            strs.Add(await iface.Foo());
        }
        return strs;
    }

    [Function]
    public CustomIface WithOptionalStr(CustomIface ifaceArg, string? strArg = null)
    {
        return ifaceArg.WithOptionalStr(strArg);
    }

    [Function]
    public CustomIface WithOptionalObj(CustomIface ifaceArg, Directory? objArg = null)
    {
        return ifaceArg.WithOptionalObj(objArg);
    }

    [Function]
    public Test WithIface(CustomIface iface)
    {
        return new Test
        {
            IfaceField = iface,
            IfaceListField = [.. IfaceListField],
            OtherIfaceListField = [.. OtherIfaceListField],
        };
    }

    [Function]
    public Test WithIfaceList(List<CustomIface> ifaces)
    {
        return new Test
        {
            IfaceField = IfaceField,
            IfaceListField = [.. ifaces],
            OtherIfaceListField = [.. OtherIfaceListField],
        };
    }

    [Function]
    public Test WithOtherIfaceList(List<OtherIface> ifaces)
    {
        return new Test
        {
            IfaceField = IfaceField,
            IfaceListField = [.. IfaceListField],
            OtherIfaceListField = [.. ifaces],
        };
    }

    [Function]
    public async Task<string> ParentIfaceFields()
    {
        var parts = new List<string>();
        if (IfaceField != null)
        {
            parts.Add(await IfaceField.Str());
        }
        foreach (var iface in IfaceListField)
        {
            parts.Add(await iface.Str());
        }
        foreach (var iface in OtherIfaceListField)
        {
            parts.Add(await iface.Foo());
        }
        return string.Join(" ", parts);
    }

    [Function]
    public CustomObj ReturnCustomObj(List<CustomIface> ifaces, List<OtherIface> otherIfaces)
    {
        return new CustomObj
        {
            Foo = "foo",
            SelfIface = ifaces[0],
            OtherIface = otherIfaces[0],
        };
    }

    [Function]
    public CustomIface WithOtherIface(CustomIface ifaceArg, OtherIface other)
    {
        return ifaceArg.WithOtherIface(other);
    }

    [Function]
    public async Task<List<OtherIface>> DynamicOtherIfaceList(CustomIface ifaceArg)
    {
        return await ifaceArg.DynamicOtherIfaceList();
    }
}

[Object]
public class CustomObj
{
    [Field]
    public string Foo { get; set; } = "";

    [Field]
    public CustomIface? SelfIface { get; set; }

    [Field]
    public OtherIface? OtherIface { get; set; }
}
