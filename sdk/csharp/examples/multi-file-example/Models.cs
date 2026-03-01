using System;
using Dagger;

/// <summary>
/// User model - demonstrates separate model file.
/// Must be marked with [Object] and use [Function] for properties to be serialized by Dagger.
/// </summary>
[Object]
public class User
{
    /// <summary>
    /// User's name
    /// </summary>
    [Function]
    public string Name { get; set; } = string.Empty;

    /// <summary>
    /// User's email address
    /// </summary>
    [Function]
    public string Email { get; set; } = string.Empty;

    /// <summary>
    /// When the user was created (ISO 8601 format)
    /// </summary>
    [Function]
    public string CreatedAt { get; set; } = string.Empty;
}

/// <summary>
/// Validation result model.
/// Must be marked with [Object] and use [Function] for Dagger serialization.
/// </summary>
[Object]
public class ValidationResult
{
    /// <summary>
    /// Whether validation passed
    /// </summary>
    [Function]
    public bool IsValid { get; set; }

    /// <summary>
    /// List of validation errors
    /// </summary>
    [Function]
    public string[] Errors { get; set; } = Array.Empty<string>();
}
