# Installation Guide

This guide covers installing and setting up the Dagger Nushell SDK.

## Prerequisites

### Required
- **Nushell**: Version 0.99.0 or later
  - Install: https://www.nushell.sh/book/installation.html
  - Verify: `nu --version`
- **Dagger CLI**: Version 0.19.0 or later
  - Install: https://docs.dagger.io/install
  - Verify: `dagger version`

### Recommended
- **Docker**: For running containers locally
  - Install: https://docs.docker.com/get-docker/
- **Git**: For version control and module dependencies

## Installing Nushell

### macOS
```bash
brew install nushell
```

### Linux
```bash
# Using cargo
cargo install nu

# Or download from releases
curl -LO https://github.com/nushell/nushell/releases/latest/download/nu-linux-x86_64.tar.gz
tar xf nu-linux-x86_64.tar.gz
sudo mv nu /usr/local/bin/
```

### Windows
```powershell
winget install nushell
```

Verify installation:
```bash
nu --version
```

## Installing Dagger CLI

### macOS
```bash
brew install dagger
```

### Linux
```bash
curl -L https://dl.dagger.io/dagger/install.sh | sh
```

### Windows
```powershell
Invoke-WebRequest -Uri https://dl.dagger.io/dagger/install.ps1 -OutFile install.ps1
powershell -ExecutionPolicy Bypass -File ./install.ps1
```

Verify installation:
```bash
dagger version
```

## Creating a New Dagger Module

Initialize a new Dagger module with the Nushell SDK:

```bash
# Create project directory
mkdir my-dagger-module
cd my-dagger-module

# Initialize with Nushell SDK
dagger init --sdk=nushell
```

This creates:
- `dagger.json` - Module configuration
- `main.nu` - Module entry point with example functions

## Project Structure

After initialization, your project structure looks like:

```
my-dagger-module/
├── dagger.json          # Module configuration
└── main.nu              # Main module file
```

### dagger.json

```json
{
  "name": "my-dagger-module",
  "sdk": "nushell",
  "source": "."
}
```

### main.nu

Your `main.nu` file contains example functions to get you started:

```nushell
# Returns a container that echoes whatever string argument is provided
export def container-echo [
    string_arg: string
] {
    dag container
    | container from "alpine:latest"
    | container with-exec ["echo", $string_arg]
}

# Returns lines that match a pattern in files
export def grep-dir [
    directory_arg: Directory
    pattern: string
] {
    dag container
    | container from "alpine:latest"
    | container with-mounted-directory "/mnt" $directory_arg
    | container with-workdir "/mnt"
    | container with-exec ["grep", "-R", $pattern, "."]
    | container stdout
}
```

## Verifying the Installation

Test that everything works:

```bash
# List available functions
dagger functions

# Call a function
dagger call container-echo --string-arg="Hello from Dagger!"

# Run checks (if defined)
dagger check
```

## Development Environment Setup

### IDE Support

**VS Code with Nushell Extension:**
1. Install [nushell-vscode-support](https://marketplace.visualstudio.com/items?itemName=TheNuProjectContributors.vscode-nushell-lang)
2. Configure settings:
```json
{
    "files.associations": {
        "*.nu": "nushell"
    }
}
```

### Shell Integration

For better development experience, add to your Nushell config (`~/.config/nushell/config.nu`):

```nushell
# Dagger completion (if available)
# source ~/.config/nushell/completions/dagger.nu

# Helpful aliases for Dagger development
alias dg = dagger
alias dgf = dagger functions
alias dgc = dagger check
```

## Troubleshooting

### Common Issues

**Error: "nu: command not found"**
- Solution: Ensure Nushell is in your PATH
- Verify: `which nu`

**Error: "dagger: command not found"**
- Solution: Ensure Dagger CLI is installed and in your PATH
- Verify: `which dagger`

**Error: "unknown SDK: nushell"**
- Solution: Update to Dagger v0.19.0 or later
- Check version: `dagger version`

**Error: "Cannot find module"**
- Solution: Run from the directory containing `dagger.json`
- Or specify module path: `dagger -m ./path/to/module functions`

### Getting Help

- **Dagger Documentation**: https://docs.dagger.io
- **Nushell Documentation**: https://www.nushell.sh/book/
- **Dagger Discord**: https://discord.gg/dagger-io
- **GitHub Issues**: https://github.com/dagger/dagger/issues

## Next Steps

- **[Quickstart Guide](quickstart.md)** - Build your first pipeline
- **[API Reference](reference.md)** - Complete function reference
- **[Examples](examples.md)** - Real-world examples
- **[Architecture](architecture.md)** - How the SDK works

## Updating

### Update Dagger CLI
```bash
# macOS
brew upgrade dagger

# Linux
curl -L https://dl.dagger.io/dagger/install.sh | sh

# Or download specific version
curl -L https://dl.dagger.io/dagger/releases/0.19.0/dagger_v0.19.0_linux_amd64.tar.gz | tar xz
```

### Update Nushell
```bash
# macOS
brew upgrade nushell

# Using cargo
cargo install nu --force
```

### Update SDK in Existing Module

The SDK version is managed through the Dagger CLI. Update your CLI to get the latest SDK:

```bash
# Update Dagger CLI
dagger version  # Check current version
# Follow update steps above

# Regenerate SDK files (if needed)
dagger develop
```
