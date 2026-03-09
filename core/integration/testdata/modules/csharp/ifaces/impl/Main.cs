using Dagger;

[Object]
public class Impl
{
    [Function]
    public string Str { get; set; } = "";

    [Function]
    public List<string> StrList { get; set; } = [];

    [Function]
    public int IntValue { get; set; }

    [Function]
    public List<int> IntList { get; set; } = [];

    [Function]
    public bool BoolValue { get; set; }

    [Function]
    public List<bool> BoolList { get; set; } = [];

    [Function]
    public Directory? Obj { get; set; }

    [Function]
    public List<Directory> ObjList { get; set; } = [];

    [Function]
    public List<OtherImpl> Others { get; set; } = [];

    [Function]
    public List<OtherIface> OtherIfaces { get; set; } = [];

    public Impl() { }

    public Impl(
        List<string> strList,
        List<int> intList,
        List<bool> boolList,
        List<Directory> objList
    )
    {
        Str = strList.FirstOrDefault() ?? "";
        StrList = strList;
        IntValue = intList.FirstOrDefault();
        IntList = intList;
        BoolValue = boolList.FirstOrDefault();
        BoolList = boolList;
        Obj = objList.FirstOrDefault();
        ObjList = objList;
    }

    [Function]
    public void Void() { }

    [Function]
    public Impl WithStr(string strArg)
    {
        return new Impl
        {
            Str = strArg,
            StrList = [.. StrList],
            IntValue = IntValue,
            IntList = [.. IntList],
            BoolValue = BoolValue,
            BoolList = [.. BoolList],
            Obj = Obj,
            ObjList = [.. ObjList],
            Others = [.. Others],
            OtherIfaces = [.. OtherIfaces],
        };
    }

    [Function]
    public Impl WithStrList(List<string> strListArg)
    {
        return new Impl
        {
            Str = Str,
            StrList = [.. strListArg],
            IntValue = IntValue,
            IntList = [.. IntList],
            BoolValue = BoolValue,
            BoolList = [.. BoolList],
            Obj = Obj,
            ObjList = [.. ObjList],
            Others = [.. Others],
            OtherIfaces = [.. OtherIfaces],
        };
    }

    [Function]
    public Impl WithInt(int intArg)
    {
        return new Impl
        {
            Str = Str,
            StrList = [.. StrList],
            IntValue = intArg,
            IntList = [.. IntList],
            BoolValue = BoolValue,
            BoolList = [.. BoolList],
            Obj = Obj,
            ObjList = [.. ObjList],
            Others = [.. Others],
            OtherIfaces = [.. OtherIfaces],
        };
    }

    [Function]
    public Impl WithIntList(List<int> intListArg)
    {
        return new Impl
        {
            Str = Str,
            StrList = [.. StrList],
            IntValue = IntValue,
            IntList = [.. intListArg],
            BoolValue = BoolValue,
            BoolList = [.. BoolList],
            Obj = Obj,
            ObjList = [.. ObjList],
            Others = [.. Others],
            OtherIfaces = [.. OtherIfaces],
        };
    }

    [Function]
    public Impl WithBool(bool boolArg)
    {
        return new Impl
        {
            Str = Str,
            StrList = [.. StrList],
            IntValue = IntValue,
            IntList = [.. IntList],
            BoolValue = boolArg,
            BoolList = [.. BoolList],
            Obj = Obj,
            ObjList = [.. ObjList],
            Others = [.. Others],
            OtherIfaces = [.. OtherIfaces],
        };
    }

    [Function]
    public Impl WithBoolList(List<bool> boolListArg)
    {
        return new Impl
        {
            Str = Str,
            StrList = [.. StrList],
            IntValue = IntValue,
            IntList = [.. IntList],
            BoolValue = BoolValue,
            BoolList = [.. boolListArg],
            Obj = Obj,
            ObjList = [.. ObjList],
            Others = [.. Others],
            OtherIfaces = [.. OtherIfaces],
        };
    }

    [Function]
    public Impl WithObj(Directory objArg)
    {
        return new Impl
        {
            Str = Str,
            StrList = [.. StrList],
            IntValue = IntValue,
            IntList = [.. IntList],
            BoolValue = BoolValue,
            BoolList = [.. BoolList],
            Obj = objArg,
            ObjList = [.. ObjList],
            Others = [.. Others],
            OtherIfaces = [.. OtherIfaces],
        };
    }

    [Function]
    public Impl WithObjList(List<Directory> objListArg)
    {
        return new Impl
        {
            Str = Str,
            StrList = [.. StrList],
            IntValue = IntValue,
            IntList = [.. IntList],
            BoolValue = BoolValue,
            BoolList = [.. BoolList],
            Obj = Obj,
            ObjList = [.. objListArg],
            Others = [.. Others],
            OtherIfaces = [.. OtherIfaces],
        };
    }

    [Function]
    public Impl SelfIface()
    {
        return WithStr(Str + "self");
    }

    [Function]
    public List<Impl> SelfIfaceList()
    {
        return [WithStr(Str + "self1"), WithStr(Str + "self2")];
    }

    [Function]
    public OtherImpl OtherIface()
    {
        return new OtherImpl { Foo = Str + "other" };
    }

    [Function]
    public List<OtherImpl> StaticOtherIfaceList()
    {
        return [new OtherImpl { Foo = Str + "other1" }, new OtherImpl { Foo = Str + "other2" }];
    }

    [Function]
    public Impl WithOptionalStr(string? strArg = null)
    {
        var result = new Impl
        {
            Str = Str,
            StrList = [.. StrList],
            IntValue = IntValue,
            IntList = [.. IntList],
            BoolValue = BoolValue,
            BoolList = [.. BoolList],
            Obj = Obj,
            ObjList = [.. ObjList],
            Others = [.. Others],
            OtherIfaces = [.. OtherIfaces],
        };
        if (strArg != null)
        {
            result.Str = strArg;
        }
        return result;
    }

    [Function]
    public Impl WithOptionalObj(Directory? objArg = null)
    {
        var result = new Impl
        {
            Str = Str,
            StrList = [.. StrList],
            IntValue = IntValue,
            IntList = [.. IntList],
            BoolValue = BoolValue,
            BoolList = [.. BoolList],
            Obj = Obj,
            ObjList = [.. ObjList],
            Others = [.. Others],
            OtherIfaces = [.. OtherIfaces],
        };
        if (objArg != null)
        {
            result.Obj = objArg;
        }
        return result;
    }

    [Function]
    public Impl WithOtherIface(OtherImpl other)
    {
        var result = new Impl
        {
            Str = Str,
            StrList = [.. StrList],
            IntValue = IntValue,
            IntList = [.. IntList],
            BoolValue = BoolValue,
            BoolList = [.. BoolList],
            Obj = Obj,
            ObjList = [.. ObjList],
            Others = [.. Others],
            OtherIfaces = [.. OtherIfaces],
        };
        result.Others.Add(other);
        return result;
    }

    [Function]
    public List<OtherImpl> DynamicOtherIfaceList()
    {
        return Others;
    }

    [Function]
    public Impl WithOtherIfaceByIface(OtherIface other)
    {
        var result = new Impl
        {
            Str = Str,
            StrList = [.. StrList],
            IntValue = IntValue,
            IntList = [.. IntList],
            BoolValue = BoolValue,
            BoolList = [.. BoolList],
            Obj = Obj,
            ObjList = [.. ObjList],
            Others = [.. Others],
            OtherIfaces = [.. OtherIfaces],
        };
        result.OtherIfaces.Add(other);
        return result;
    }

    [Function]
    public List<OtherIface> DynamicOtherIfaceByIfaceList()
    {
        return OtherIfaces;
    }
}

[Interface]
public interface OtherIface
{
    [Function]
    Task<string> Foo();
}

[Object]
public class OtherImpl
{
    [Function]
    public string Foo { get; set; } = "";

    public OtherImpl() { }
}
