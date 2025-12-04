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

### DAGGER008: Invalid Cache value in Function attribute

**Severity:** Warning

Validates that the `Cache` property in `[Function]` attribute uses valid values.

```csharp
// ❌ Error: Invalid cache value
[Function(Cache = "invalid")]
public string Hello() => "world";

// ✅ Correct: Use "never", "session", or duration strings
[Function(Cache = "never")]
[Function(Cache = "session")]
[Function(Cache = "5m")]    // 5 minutes
[Function(Cache = "1h")]    // 1 hour
[Function(Cache = "30s")]   // 30 seconds
[Function(Cache = "2h30m")] // 2 hours 30 minutes
public string Hello() => "world";
```

### DAGGER009: [Ignore] attribute can only be applied to Directory parameters

**Severity:** Error

Prevents using `[Ignore]` attribute on non-Directory parameters, which would cause runtime errors.

```csharp
// ❌ Error: [Ignore] only valid on Directory parameters
[Function]
public string Build([Ignore("*.log")] string name) { }

// ✅ Correct
[Function]
public async Task Build(
    [Ignore("*.log")]
    Directory source) { }
```

### DAGGER010: [DefaultPath] attribute can only be applied to Directory or File parameters

**Severity:** Error

Prevents using `[DefaultPath]` attribute on invalid parameter types.

```csharp
// ❌ Error: [DefaultPath] only valid on Directory/File parameters
[Function]
public string Build([DefaultPath(".")] string name) { }

// ✅ Correct
[Function]
public async Task Build(
    [DefaultPath(".")]
    Directory source) { }
```

### DAGGER011: Dagger module requires dagger.json configuration file

**Severity:** Warning

Ensures that Dagger modules have a required `dagger.json` configuration file.

```csharp
// ❌ Warning: No dagger.json found
[Object]
public class MyModule { }
```

**Resolution:** Run `dagger init --sdk=csharp` to create a `dagger.json` file for your module.

### DAGGER012: Module class name should match dagger.json module name

**Severity:** Error  
**Code Fix:** ✅ Available (renames class)

Ensures the module class name matches the PascalCase transformation of the `dagger.json` module name.

```csharp
// dagger.json: { "name": "my-module" }

// ❌ Error: Class name doesn't match
[Object]
public class SomethingElse { }

// ✅ Correct: Matches PascalCase of "my-module"
[Object]
public class MyModule { }
```

**Naming Convention:**
- `my-module` → `MyModule`
- `api-gateway` → `ApiGateway`
- `my_service` → `MyService`
- `hello-world` → `HelloWorld`

**Code Fix:** Use the 💡 icon to automatically rename the class and update all references.

### DAGGER013: Project file name should match dagger.json module name

**Severity:** Warning

Recommends that the `.csproj` file name matches the module name from `dagger.json`.

```csharp
// dagger.json: { "name": "my-module" }

// ❌ Warning: Project file is "WrongName.csproj"
// ✅ Correct: Project file should be "MyModule.csproj"
```

**Resolution:** Manually rename the `.csproj` file to match the expected name. This requires closing and reopening the solution in your IDE.

## Usage

The analyzers are automatically included when you reference the `Dagger.SDK` NuGet package. They run in the IDE (Visual Studio, VS Code with C# extension, Rider) and provide real-time feedback as you write your Dagger modules.

### Automatic Configuration

When you run `dagger init --sdk=csharp`, the generated `.csproj` file automatically includes the correct path to `dagger.json`:

```xml
<ItemGroup>
  <!-- Path is automatically calculated based on --source flag -->
  <AdditionalFiles Include="dagger.json" />
  <!-- or "../dagger.json" or "../../dagger.json" etc. -->
</ItemGroup>
```

This enables the module configuration analyzers (DAGGER011-013) to validate your module structure without any manual setup. The path is determined during initialization based on your module's source directory relative to `dagger.json`.

## Configuration

You can customize analyzer severity and behavior using the `.editorconfig` file that's automatically created with your module. The file includes commented examples for all Dagger analyzers.

### Individual Rule Configuration

Configure specific rules by their diagnostic ID:

```ini
[*.cs]
# Disable a specific analyzer
dotnet_diagnostic.DAGGER001.severity = none

# Make a suggestion into a warning
dotnet_diagnostic.DAGGER002.severity = warning

# Make a warning into an error (blocks build)
dotnet_diagnostic.DAGGER011.severity = error
```

### Category-Level Configuration

Configure all Dagger analyzers at once:

```ini
[*.cs]
# Make all Dagger analyzers warnings by default
dotnet_analyzer_diagnostic.category-Dagger.severity = warning

# Then override specific rules as needed
dotnet_diagnostic.DAGGER012.severity = error
```

### Severity Levels

| Severity | Description | IDE Behavior | Build Behavior |
|----------|-------------|--------------|----------------|
| `none` | Completely disabled | Not shown | Not reported |
| `silent` | Runs but hidden | Not shown (code fixes still available) | Not reported |
| `suggestion` | Low priority | Gray dots/underlines | Reported in Messages |
| `warning` | Medium priority | Green squiggles | Build warnings |
| `error` | High priority | Red squiggles | Build errors (fails build) |

### Common Configuration Scenarios

**Disable all documentation analyzers:**
```ini
[*.cs]
dotnet_diagnostic.DAGGER002.severity = none  # Function documentation
dotnet_diagnostic.DAGGER003.severity = none  # Parameter documentation
dotnet_diagnostic.DAGGER006.severity = none  # Class documentation
dotnet_diagnostic.DAGGER007.severity = none  # Field documentation
```

**Make module naming errors strict:**
```ini
[*.cs]
dotnet_diagnostic.DAGGER012.severity = error  # Class name mismatch
dotnet_diagnostic.DAGGER013.severity = error  # Project name mismatch
```

**Disable all Dagger analyzers:**
```ini
[*.cs]
dotnet_analyzer_diagnostic.category-Dagger.severity = none
```

For more information, see Microsoft's [EditorConfig documentation](https://learn.microsoft.com/en-us/dotnet/fundamentals/code-analysis/configuration-files).

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
