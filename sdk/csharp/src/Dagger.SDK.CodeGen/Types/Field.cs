using System;
using System.Collections.Immutable;
using System.Linq;
using System.Text.Json.Serialization;

namespace Dagger.SDK.CodeGen.Types;

public class Field
{
    [JsonPropertyName("name")]
    public required string Name { get; set; }

    [JsonPropertyName("description")]
    public required string Description { get; set; }

    [JsonPropertyName("type")]
    public required TypeRef Type { get; set; }

    [JsonPropertyName("args")]
    public required InputValue[] Args { get; set; }

    [JsonPropertyName("isDeprecated")]
    public bool IsDeprecated { get; set; }

    [JsonPropertyName("deprecationReason")]
    public required string DeprecationReason { get; set; }

    [JsonPropertyName("directives")]
    public Directive[]? Directives { get; set; }

    /// <summary>
    /// The parent type that owns this field.
    /// Used to detect ID fields and perform parent object lookups.
    /// </summary>
    [JsonIgnore]
    public Type? ParentType { get; set; }

    /// <summary>
    /// Get optional arguments from Args.
    /// </summary>
    public ImmutableArray<InputValue> OptionalArgs() =>
        [.. Args.Where(arg => arg.Type.Kind != TypeKind.NON_NULL)];

    /// <summary>
    /// Get required arguments from Args.
    /// </summary>
    public ImmutableArray<InputValue> RequiredArgs() =>
        [.. Args.Where(arg => arg.Type.Kind == TypeKind.NON_NULL)];

    /// <summary>
    /// Checks if this field provides an ID for the parent object.
    /// </summary>
    /// <returns>True if this field returns the ID type and has no required arguments.</returns>
    public bool ProvidesId()
    {
        if (ParentType == null)
        {
            return false;
        }

        // ID fields have no required arguments and return ID! type
        // The type structure is: NonNull -> Scalar(ID)
        var hasNoRequiredArgs = RequiredArgs().Length == 0;
        var isIdType = false;

        if (Type.Kind == TypeKind.NON_NULL && Type.OfType != null)
        {
            isIdType = Type.OfType.Kind == TypeKind.SCALAR && Type.OfType.Name == "ID";
        }

        return hasNoRequiredArgs && isIdType;
    }

    /// <summary>
    /// Gets the ID field from the parent object type.
    /// </summary>
    /// <returns>The field that provides the ID for the parent object.</returns>
    /// <exception cref="InvalidOperationException">Thrown when no ID field is found.</exception>
    public Field GetIdField()
    {
        if (ParentType?.Fields == null)
        {
            throw new InvalidOperationException(
                "Cannot get ID field: ParentType or Fields is null"
            );
        }

        var idField = ParentType.Fields.FirstOrDefault(f => f.ProvidesId());
        if (idField == null)
        {
            throw new InvalidOperationException($"No ID field found on type {ParentType.Name}");
        }

        return idField;
    }
}
