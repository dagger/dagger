using System;
using System.Collections.Generic;
using System.Linq;

/// <summary>
/// User service - demonstrates business logic in separate file.
/// This is an internal service class and doesn't need Dagger attributes.
/// </summary>
public class UserService
{
    /// <summary>
    /// Creates a new user with the specified name and email.
    /// </summary>
    /// <param name="name">The name of the user.</param>
    /// <param name="email">The email of the user.</param>
    /// <returns>A new User object with the specified details.</returns>
    public User CreateUser(string name, string email)
    {
        return new User
        {
            Name = name,
            Email = email,
            CreatedAt = DateTime.UtcNow.ToString("yyyy-MM-dd HH:mm:ss"),
        };
    }

    /// <summary>
    /// Formats a user's details into a string.
    /// </summary>
    /// <param name="user">The user to format.</param>
    /// <returns>A formatted string representing the user's details.</returns>
    public string FormatUser(User user)
    {
        return $"{user.Name} <{user.Email}> (created: {user.CreatedAt})";
    }
}

/// <summary>
/// Validation service - demonstrates separation of concerns.
/// Internal service, no Dagger attributes needed.
/// </summary>
public class UserValidator
{
    /// <summary>
    /// Validates the given user object.
    /// </summary>
    /// <param name="user">The user to validate.</param>
    public ValidationResult Validate(User user)
    {
        var errors = new List<string>();

        if (string.IsNullOrWhiteSpace(user.Name))
        {
            errors.Add("Name is required");
        }

        if (string.IsNullOrWhiteSpace(user.Email))
        {
            errors.Add("Email is required");
        }
        else if (!user.Email.Contains("@"))
        {
            errors.Add("Email must be valid");
        }

        return new ValidationResult { IsValid = errors.Count == 0, Errors = errors.ToArray() };
    }
}

/// <summary>
/// Report builder - demonstrates helper classes.
/// Internal service, no Dagger attributes needed.
/// </summary>
public class ReportBuilder
{
    /// <summary>
    /// Builds a report summarizing the users and their validation results.
    /// </summary>
    /// <param name="users">Array of users to include in the report.</param>
    /// <param name="results">Array of validation results corresponding to the users.</param>
    /// <returns>A formatted string report.</returns>
    public string BuildReport(User[] users, ValidationResult[] results)
    {
        var timestamp = DateTime.UtcNow.ToString("yyyy-MM-dd HH:mm:ss");
        var report = $"User Report ({timestamp})\n";
        report += $"Total users: {users.Length}\n";
        report += $"Valid users: {results.Count(r => r.IsValid)}\n";
        report += $"Invalid users: {results.Count(r => !r.IsValid)}\n\n";

        for (int i = 0; i < users.Length; i++)
        {
            report += $"{i + 1}. {users[i].Name} - ";
            report += results[i].IsValid
                ? "✓ Valid\n"
                : $"✗ Invalid: {string.Join(", ", results[i].Errors)}\n";
        }

        return report;
    }
}
