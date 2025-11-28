using System;
using System.Linq;
using Dagger;

/// <summary>
/// Main module demonstrating multi-file organization.
/// Main.cs contains the Dagger API surface, while Models.cs and Services.cs contain supporting types.
/// </summary>
[Object]
public class MultiFileExample
{
    private readonly UserService _userService = new UserService();
    private readonly UserValidator _validator = new UserValidator();
    private readonly ReportBuilder _reportBuilder = new ReportBuilder();

    /// <summary>
    /// Creates a new user using the User model from Models.cs.
    /// The User class is marked with [Object] so it can be returned from functions.
    /// </summary>
    /// <param name="name">User's name</param>
    /// <param name="email">User's email address</param>
    [Function]
    public User CreateUser(string name, string email)
    {
        return _userService.CreateUser(name, email);
    }

    /// <summary>
    /// Formats a user object into a string.
    /// Demonstrates accepting a custom [Object] type as a parameter.
    /// </summary>
    /// <param name="user">The user to format</param>
    [Function]
    public string FormatUser(User user)
    {
        return _userService.FormatUser(user);
    }

    /// <summary>
    /// Validates a user object and returns validation results.
    /// Both User and ValidationResult are marked with [Object] for serialization.
    /// </summary>
    /// <param name="user">The user to validate</param>
    [Function]
    public ValidationResult ValidateUser(User user)
    {
        return _validator.Validate(user);
    }

    /// <summary>
    /// Creates and validates multiple users, then generates a report.
    /// Demonstrates working with arrays of custom types.
    /// </summary>
    [Function]
    public string ProcessUsers()
    {
        var users = new[]
        {
            _userService.CreateUser("Alice", "alice@example.com"),
            _userService.CreateUser("Bob", "bob@invalid"),
            _userService.CreateUser("Charlie", "charlie@example.com"),
            _userService.CreateUser("", "no-name@example.com")
        };

        var results = users.Select(u => _validator.Validate(u)).ToArray();

        return _reportBuilder.BuildReport(users, results);
    }
}
