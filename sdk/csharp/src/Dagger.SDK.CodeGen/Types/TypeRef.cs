using System;
using System.Text.Json.Serialization;

namespace Dagger.SDK.CodeGen.Types;

public class TypeRef
{
    [JsonPropertyName("kind")]
    public required TypeKind Kind { get; set; }

    [JsonPropertyName("name")]
    public string? Name { get; set; }

    [JsonPropertyName("ofType")]
    public TypeRef? OfType { get; set; }

    public bool IsLeaf()
    {
        var tr = this;

        if (Kind == TypeKind.NON_NULL && OfType != null)
        {
            tr = OfType;
        }

        if (tr.Kind == TypeKind.ENUM)
        {
            return true;
        }

        if (tr.Kind == TypeKind.SCALAR)
        {
            return true;
        }

        return false;
    }

    public bool IsList()
    {
        var tr = this;

        if (Kind == TypeKind.NON_NULL && OfType != null)
        {
            tr = OfType;
        }

        if (tr.Kind == TypeKind.LIST)
        {
            return true;
        }

        return false;
    }

    public bool IsEnum()
    {
        var tr = this;

        if (Kind == TypeKind.NON_NULL && OfType != null)
        {
            tr = OfType;
        }

        if (tr.Kind == TypeKind.ENUM)
        {
            return true;
        }

        return false;
    }

    public bool IsInputObject()
    {
        var tr = this;

        if (Kind == TypeKind.NON_NULL && OfType != null)
        {
            tr = OfType;
        }

        if (tr.Kind == TypeKind.INPUT_OBJECT)
        {
            return true;
        }

        return false;
    }

    public bool IsScalar()
    {
        var tr = this;

        if (Kind == TypeKind.NON_NULL && OfType != null)
        {
            tr = OfType;
        }

        if (tr.Kind == TypeKind.SCALAR)
        {
            return true;
        }

        return false;
    }

    public bool IsObject()
    {
        var tr = this;

        if (Kind == TypeKind.NON_NULL && OfType != null)
        {
            tr = OfType;
        }

        if (tr.Kind == TypeKind.OBJECT)
        {
            return true;
        }

        return false;
    }

    public bool IsInterface()
    {
        var tr = this;

        if (Kind == TypeKind.NON_NULL && OfType != null)
        {
            tr = OfType;
        }

        if (tr.Kind == TypeKind.INTERFACE)
        {
            return true;
        }

        return false;
    }

    public TypeRef GetType_()
    {
        var tr = this;

        if (Kind == TypeKind.NON_NULL && OfType != null)
        {
            tr = OfType;
        }

        return tr;
    }

    public string Describe_()
    {
        return $" [{Name} {Kind}{OfType?.Describe_()}]";
    }
}
