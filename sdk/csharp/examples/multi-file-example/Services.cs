using System;
using System.Collections.Generic;
using System.Linq;

/// <summary>
/// User service - demonstrates business logic in separate file.
/// This is an internal service class and doesn't need Dagger attributes.
/// </summary>
public class UserService
{
    public User CreateUser(string name, string email)
    {
        return new User
        {
            Name = name,
            Email = email,
            CreatedAt = DateTime.UtcNow.ToString("yyyy-MM-dd HH:mm:ss")
        };
    }

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

        return new ValidationResult
        {
            IsValid = errors.Count == 0,
            Errors = errors.ToArray()
        };
    }
}

/// <summary>
/// Report builder - demonstrates helper classes.
/// Internal service, no Dagger attributes needed.
/// </summary>
public class ReportBuilder
{
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
            report += results[i].IsValid ? "✓ Valid\n" : $"✗ Invalid: {string.Join(", ", results[i].Errors)}\n";
        }

        return report;
    }
}
