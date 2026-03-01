namespace Dagger;

/// <summary>
/// Base class for Dagger scalar ID types.
/// </summary>
public class Scalar
{
    /// <summary>
    /// The scalar ID value.
    /// </summary>
    public string Value { get; set; } = string.Empty;

    /// <summary>
    /// Returns the scalar ID value as a string.
    /// </summary>
    public override string ToString() => Value;
}
