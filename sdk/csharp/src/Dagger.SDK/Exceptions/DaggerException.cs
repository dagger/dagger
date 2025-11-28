using Dagger.GraphQL;

namespace Dagger.Exceptions;

/// <summary>
/// Base exception for all Dagger-related errors.
/// </summary>
public class DaggerException : Exception
{
    /// <summary>
    /// Initializes a new Dagger exception with a message.
    /// </summary>
    /// <param name="message">The error message.</param>
    public DaggerException(string message)
        : base(message) { }

    /// <summary>
    /// Initializes a new Dagger exception with a message and inner exception.
    /// </summary>
    /// <param name="message">The error message.</param>
    /// <param name="innerException">The inner exception.</param>
    public DaggerException(string message, Exception innerException)
        : base(message, innerException) { }
}

/// <summary>
/// Exception thrown when a GraphQL query returns an error.
/// </summary>
public class QueryException : DaggerException
{
    /// <summary>
    /// Gets the GraphQL errors returned by the server.
    /// </summary>
    public IReadOnlyList<GraphQLError> Errors { get; }

    /// <summary>
    /// Gets the GraphQL query that caused the error.
    /// </summary>
    public string Query { get; }

    /// <summary>
    /// Initializes a new query exception.
    /// </summary>
    /// <param name="message">The error message.</param>
    /// <param name="errors">The GraphQL errors.</param>
    /// <param name="query">The query that caused the error.</param>
    public QueryException(string message, IReadOnlyList<GraphQLError> errors, string query)
        : base(message)
    {
        Errors = errors;
        Query = query;
    }

    /// <summary>
    /// Initializes a new query exception with an inner exception.
    /// </summary>
    /// <param name="message">The error message.</param>
    /// <param name="errors">The GraphQL errors.</param>
    /// <param name="query">The query that caused the error.</param>
    /// <param name="innerException">The inner exception.</param>
    public QueryException(
        string message,
        IReadOnlyList<GraphQLError> errors,
        string query,
        Exception innerException
    )
        : base(message, innerException)
    {
        Errors = errors;
        Query = query;
    }
}

/// <summary>
/// Exception thrown when an exec operation fails.
/// </summary>
public class ExecException : QueryException
{
    /// <summary>
    /// Gets the command that was executed.
    /// </summary>
    public IReadOnlyList<string> Command { get; }

    /// <summary>
    /// Gets the exit code of the command.
    /// </summary>
    public int ExitCode { get; }

    /// <summary>
    /// Gets the stdout output of the command.
    /// </summary>
    public string Stdout { get; }

    /// <summary>
    /// Gets the stderr output of the command.
    /// </summary>
    public string Stderr { get; }

    /// <summary>
    /// Initializes a new exec exception.
    /// </summary>
    /// <param name="message">The error message.</param>
    /// <param name="errors">The GraphQL errors.</param>
    /// <param name="query">The query that caused the error.</param>
    /// <param name="command">The command that was executed.</param>
    /// <param name="exitCode">The exit code.</param>
    /// <param name="stdout">The stdout output.</param>
    /// <param name="stderr">The stderr output.</param>
    public ExecException(
        string message,
        IReadOnlyList<GraphQLError> errors,
        string query,
        IReadOnlyList<string> command,
        int exitCode,
        string stdout,
        string stderr
    )
        : base(message, errors, query)
    {
        Command = command;
        ExitCode = exitCode;
        Stdout = stdout;
        Stderr = stderr;
    }

    /// <summary>
    /// Returns a string representation of the exception including command details.
    /// </summary>
    /// <returns>A formatted string with command, exit code, stdout, and stderr.</returns>
    public override string ToString()
    {
        var cmdStr = string.Join(" ", Command);
        return $"{Message}\nCommand: {cmdStr}\nExit Code: {ExitCode}\nStdout: {Stdout}\nStderr: {Stderr}";
    }
}
