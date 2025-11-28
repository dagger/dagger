# Dagger SDK Analyzers

Roslyn analyzers and code fix providers that provide IDE support and best practices guidance for developing Dagger modules in C#.

## Features

- **Real-time diagnostics**: Get instant feedback as you write your Dagger modules
- **Automatic code fixes**: Apply suggested fixes with a single click (💡 icon in IDE)
- **Best practices**: Learn recommended patterns for Dagger modules
- **Documentation helpers**: Encourages comprehensive XML documentation

## Analyzers

### DAGGER001: Public method in Dagger Object should have [Function] attribute

**Severity:** Info  
**Code Fix:** ✅ Available

Suggests adding `[Function]` attribute to public methods in classes marked with `[Object]`.

```csharp
[Object]
public class MyModule
{
    // ❌ Warning: Missing [Function] attribute
    public string Hello() => "world";
    
    // ✅ Correct
    [Function]
    public string Goodbye() => "farewell";
}
```

### DAGGER002: Dagger function should have XML documentation

**Severity:** Info  
**Code Fix:** ✅ Available

Recommends adding XML documentation to functions marked with `[Function]`. This documentation appears in `dagger functions` output.

```csharp
// ❌ Warning: Missing XML documentation
[Function]
public string Hello() => "world";

// ✅ Correct
/// <summary>
/// Returns a greeting message
/// </summary>
[Function]
public string Hello() => "world";
```

### DAGGER003: Dagger function parameter should have XML documentation

**Severity:** Info

Recommends adding XML `<param>` documentation for function parameters.

```csharp
// ❌ Warning: Missing parameter documentation
/// <summary>Greets someone</summary>
[Function]
public string Hello(string name) => $"Hello, {name}!";

// ✅ Correct
/// <summary>Greets someone</summary>
/// <param name="name">The name to greet</param>
[Function]
public string Hello(string name) => $"Hello, {name}!";
```

### DAGGER004: Directory parameter should consider [DefaultPath] attribute

**Severity:** Info  
**Code Fix:** ✅ Available

Suggests using `[DefaultPath]` attribute on `Directory` parameters to specify the default source path.

```csharp
// ❌ Suggestion: Consider adding [DefaultPath]
[Function]
public async Task Build(Directory source) { }

// ✅ Better
[Function]
public async Task Build(
    [DefaultPath(".")]
    Directory source) { }
```

### DAGGER005: Directory parameter should consider [Ignore] attribute

**Severity:** Info  
**Code Fix:** ✅ Available

Suggests using `[Ignore]` attribute on `Directory` parameters to exclude unwanted files.

```csharp
// ❌ Suggestion: Consider adding [Ignore]
[Function]
public async Task Build(
    [DefaultPath(".")]
    Directory source) { }

// ✅ Better
[Function]
public async Task Build(
    [DefaultPath(".")]
    [Ignore("node_modules", ".git", "**/*.log")]
    Directory source) { }
```

### DAGGER006: Dagger Object should have XML documentation

**Severity:** Info  
**Code Fix:** ✅ Available

Recommends adding XML documentation to classes marked with `[Object]`.

```csharp
// ❌ Warning: Missing documentation
[Object]
public class MyModule { }

// ✅ Correct
/// <summary>
/// My Dagger module for building applications
/// </summary>
[Object]
public class MyModule { }
```

### DAGGER007: Dagger field should have XML documentation

**Severity:** Info  
**Code Fix:** ✅ Available

Recommends adding XML documentation to properties marked with `[Field]`.

```csharp
// ❌ Warning: Missing documentation
[Field]
public string Version { get; set; }

// ✅ Correct
/// <summary>
/// The version to build
/// </summary>
[Field]
public string Version { get; set; }
```

## Usage

The analyzers are automatically included when you reference the `Dagger.SDK` NuGet package. They run in the IDE (Visual Studio, VS Code with C# extension, Rider) and provide real-time feedback as you write your Dagger modules.

## Configuration

You can configure analyzer severity in your `.editorconfig`:

```ini
# Disable specific analyzer
dotnet_diagnostic.DAGGER001.severity = none

# Make analyzer a warning instead of info
dotnet_diagnostic.DAGGER002.severity = warning

# Make analyzer an error
dotnet_diagnostic.DAGGER003.severity = error
```

## Benefits

- **IDE Integration**: Get real-time feedback while coding
- **Best Practices**: Learn recommended patterns for Dagger modules
- **Documentation**: Encourages comprehensive XML documentation
- **Code Quality**: Helps maintain consistent module structure
