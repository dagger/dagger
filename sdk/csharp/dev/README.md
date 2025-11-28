# C# SDK Dev Module

This Dagger module provides development utilities for building, testing, and maintaining the Dagger C# SDK.

## Purpose

The dev module automates SDK development tasks in a consistent, reproducible environment:

- **Code Generation**: Generate client code from GraphQL introspection
- **Testing**: Run unit and integration tests
- **Linting**: Check code quality and style
- **Formatting**: Auto-format code to maintain consistency
- **Packaging**: Build NuGet packages for distribution

## Available Commands

### Build and Package

```bash
# Build the SDK
dagger call -m dev build

# Create NuGet package
dagger call -m dev pack export --path=./packages
```

### Testing

```bash
# Run all tests
dagger call -m dev test --introspection-json=../src/introspection.json

# Run specific test suite
dagger call -m dev test-unit
dagger call -m dev test-integration
```

### Code Quality

```bash
# Check for linting violations
dagger call -m dev lint

# Auto-format code
dagger call -m dev format export --path=.

# Run analyzers
dagger call -m dev analyze
```

### Development Workflow

```bash
# Full development cycle
dagger call -m dev \
  format export --path=. \
  && dagger call -m dev lint \
  && dagger call -m dev test --introspection-json=../src/introspection.json
```

## How It Works

The dev module uses Dagger to create reproducible build environments:

1. **Isolated Containers**: Each command runs in a .NET 10 container
2. **Caching**: Dependencies and build artifacts are cached
3. **Consistency**: Same environment for all developers and CI/CD

This ensures that "works on my machine" issues are eliminated.

## For Contributors

When adding new features to the SDK:

1. Write tests in `src/Dagger.SDK.Tests/`
2. Run `dagger call -m dev test` to verify
3. Run `dagger call -m dev lint` to check style
4. Run `dagger call -m dev format export --path=.` to auto-format

The dev module handles all build dependencies automatically.
