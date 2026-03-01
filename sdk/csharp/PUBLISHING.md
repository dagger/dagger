# Publishing the Dagger C# SDK to NuGet

This guide explains how to build and publish the DaggerIO NuGet package.

## Overview

The Dagger C# SDK serves two purposes:

1. **Module Runtime**: Source-based distribution for Dagger modules
2. **Client Library**: NuGet package for programmatic use

This document covers publishing the **Client Library** to NuGet.org.

## Prerequisites

- .NET 10 SDK installed
- NuGet API key from nuget.org
- Maintainer access to the DaggerIO package

## Building the Package

### Option 1: Using the Dev Module (Recommended)

```bash
# From repository root
dagger call -m toolchains/csharp-sdk-dev pack export --path=packages

# This creates:
# packages/DaggerIO.0.1.0.nupkg
# packages/DaggerIO.0.1.0.snupkg (symbols)
```

### Option 2: Using dotnet CLI

```bash
cd sdk/csharp/src

# Build the package
dotnet pack Dagger.SDK/Dagger.SDK.csproj \
    -c Release \
    -o ../../packages

# Output:
# DaggerIO.0.1.0.nupkg
# DaggerIO.0.1.0.snupkg
```

## Versioning

The package version is defined in `Dagger.SDK.csproj`:

```xml
<PropertyGroup>
  <Version>0.1.0</Version>
</PropertyGroup>
```

Update this before building a new release. Follow [Semantic Versioning](https://semver.org/):

- `0.x.x` - Experimental/pre-release
- `1.0.0` - First stable release
- `1.x.x` - Minor updates (new features, backward compatible)
- `x.0.0` - Major updates (breaking changes)

## Testing the Package Locally

Before publishing, test the package locally to ensure it works correctly.

### Option 1: Dry Run with dotnet pack

Verify the package builds without errors:

```bash
cd sdk/csharp/src

# Build without publishing
dotnet pack Dagger.SDK/Dagger.SDK.csproj \
    -c Release \
    -o ../../packages \
    /p:ContinuousIntegrationBuild=true

# Inspect the package contents
dotnet nuget verify packages/DaggerIO.0.1.0.nupkg
```

### Option 2: Test in a Local Project

```bash
# Create a test project
mkdir test-nuget
cd test-nuget
dotnet new console

# Add the local package
dotnet add package DaggerIO \
    --source ../sdk/csharp/packages

# Test it
cat > Program.cs << 'EOF'
using Dagger;

var result = await Dag
    .Container()
    .From("alpine:latest")
    .WithExec(new[] { "echo", "Hello!" })
    .Stdout();

Console.WriteLine(result);
EOF

# Run with dagger
dagger run dotnet run
```

### Option 3: Inspect Package Locally

```bash
# Extract and examine package contents
unzip -l packages/DaggerIO.0.1.0.nupkg

# Verify includes:
# - lib/net10.0/Dagger.SDK.dll
# - lib/net10.0/Dagger.SDK.xml
# - analyzers/dotnet/cs/Dagger.SDK.Analyzers.dll
# - analyzers/dotnet/cs/Dagger.SDK.CodeFixes.dll
# - README.md
```

## Publishing to NuGet.org

### 1. Get API Key

1. Sign in to [nuget.org](https://www.nuget.org/)
2. Go to **Account Settings** → **API Keys**
3. Create a new API key with:
   - **Package Owner**: Dagger
   - **Scopes**: Push new packages and package versions
   - **Glob Pattern**: `DaggerIO*`

### 2. Publish the Package

```bash
# Set your API key (one-time setup)
export NUGET_API_KEY="your-api-key-here"

# Push to NuGet.org
dotnet nuget push \
    packages/DaggerIO.0.1.0.nupkg \
    --api-key $NUGET_API_KEY \
    --source https://api.nuget.org/v3/index.json
```

### 3. Verify Publication

1. Visit https://www.nuget.org/packages/DaggerIO
2. Verify the version appears
3. Check that README and metadata display correctly

## CI/CD Integration

### Automated Publishing (Future)

```yaml
# .github/workflows/publish-csharp-sdk.yml
name: Publish C# SDK

on:
  push:
    tags:
      - 'sdk/csharp/v*'

jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      
      - name: Setup .NET
        uses: actions/setup-dotnet@v4
        with:
          dotnet-version: '10.0.x'
      
      - name: Build Package
        run: |
          cd sdk/csharp/src
          dotnet pack Dagger.SDK/Dagger.SDK.csproj -c Release
      
      - name: Publish to NuGet
        run: |
          dotnet nuget push \
            sdk/csharp/src/bin/Release/DaggerIO.*.nupkg \
            --api-key ${{ secrets.NUGET_API_KEY }} \
            --source https://api.nuget.org/v3/index.json
```

## Release Checklist

Before each release:

- [ ] Update version in `Dagger.SDK.csproj`
- [ ] Update `CHANGELOG.md` with changes
- [ ] Run all tests: `dagger call -m toolchains/csharp-sdk-dev test`
- [ ] Build package: `dagger call -m toolchains/csharp-sdk-dev pack export --path=packages`
- [ ] Test package locally (see above)
- [ ] Tag the release: `git tag sdk/csharp/v0.1.0`
- [ ] Push tag: `git push origin sdk/csharp/v0.1.0`
- [ ] Publish to NuGet
- [ ] Verify package on nuget.org
- [ ] Update GitHub release notes

## Package Contents

The published NuGet package includes:

```text
DaggerIO.0.1.0.nupkg
├── lib/
│   └── net10.0/
│       ├── Dagger.SDK.dll
│       └── Dagger.SDK.xml (API docs)
├── analyzers/
│   └── dotnet/
│       └── cs/
│           ├── Dagger.SDK.Analyzers.dll (Roslyn analyzer)
│           └── Dagger.SDK.CodeFixes.dll (code fixes)
├── README.md
└── [metadata]
```

## Troubleshooting

### Package already exists

If you see "package version already exists" error:

```bash
# Increment the version in Dagger.SDK.csproj
# Then rebuild and republish
```

### Symbol package fails

If the symbol package (`.snupkg`) fails to upload:

```bash
# Symbols are optional, you can skip them
dotnet nuget push packages/DaggerIO.0.1.0.nupkg \
    --skip-duplicate \
    --api-key $NUGET_API_KEY \
    --source https://api.nuget.org/v3/index.json
```

### Permission denied

Ensure your API key has the correct scopes:

- Push new packages and package versions
- Pattern matches `DaggerIO*`

## Support

For questions about publishing:

- Check [NuGet documentation](https://learn.microsoft.com/en-us/nuget/)
- Ask in [Dagger Discord](https://discord.gg/dagger-io)
- File an issue on [GitHub](https://github.com/dagger/dagger)
