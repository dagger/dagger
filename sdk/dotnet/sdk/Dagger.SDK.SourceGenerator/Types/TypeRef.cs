using System;
using System.Text.Json.Serialization;

namespace Dagger.SDK.SourceGenerator.Types;

public class TypeRef
{
    // TODO: use TypeKind.
    [JsonPropertyName("kind")]
    public required string Kind { get; set; }

    [JsonPropertyName("name")]
    public string Name { get; set; } = "";

    [JsonPropertyName("ofType")]
    public TypeRef? OfType { get; set; } = null;

    public bool IsLeaf()
    {
        var tr = this;

        if (Kind == "NON_NULL")
        {
            tr = OfType!;
        }

        if (tr.Kind == "ENUM")
        {
            return true;
        }

        if (tr.Kind == "SCALAR")
        {
            return true;
        }

        return false;
    }

    public bool IsList()
    {
        var tr = this;

        if (Kind == "NON_NULL")
        {
            tr = OfType!;
        }

        if (tr.Kind == "LIST")
        {
            return true;
        }

        return false;
    }

    public bool IsEnum()
    {
        var tr = this;

        if (Kind == "NON_NULL")
        {
            tr = OfType!;
        }

        if (tr.Kind == "ENUM")
        {
            return true;
        }

        return false;
    }

    public bool IsInputObject()
    {
        var tr = this;

        if (Kind == "NON_NULL")
        {
            tr = OfType!;
        }

        if (tr.Kind == "INPUT_OBJECT")
        {
            return true;
        }

        return false;
    }

    public bool IsScalar()
    {
        var tr = this;

        if (Kind == "NON_NULL")
        {
            tr = OfType!;
        }

        if (tr.Kind == "SCALAR")
        {
            return true;
        }

        return false;
    }

    public bool IsObject()
    {
        var tr = this;

        if (Kind == "NON_NULL")
        {
            tr = OfType!;
        }

        if (tr.Kind == "OBJECT")
        {
            return true;
        }

        return false;
    }

    public TypeRef GetType_()
    {
        var tr = this;

        if (Kind == "NON_NULL")
        {
            tr = OfType!;
        }

        return tr;
    }

    public String Describe_()
    {
        return $" [{Name} {Kind}{OfType?.Describe_()}]";
    }
}
