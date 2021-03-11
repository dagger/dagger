
# Stacks

## What is a stack?

Every dagger operation happens inside a *stack*.
Each stack is an isolated sandbox, with its own state and execution environment.

A stack is made of 3 layers:

1. Base configuration
2. Input values
3. Output values

## Automatic stack selection

When a stack is not explicitly selected with `--stack`, dagger automatically selects *all stacks* which are connected to the current directory.
This includes:

1. Stacks using the current directory as an input artifact (see `dagger input dir`)
2. Stacks using the current directory as an output artifact (see `dagger output dir`)
3. Stacks using the current directory as a base configuration (see `dagger base dir`)

Dagger then attempts to apply the command to all selected stacks in parallel. If there are more than one
selected stack, and the command does not support multiple stacks, the command may fail to execute.

Example:

```
$ cd ./src
$ dagger up
3 stacks selected: acme-prod, acme-dev, acme-staging. Confirm? [Yn]
Bringing acme-prod online
Bringing acme-dev online
Bringing acme-staging online
...
```

```
$ cd ./src
$ dagger query www.url
3 stacks selected: acme-prod, acme-dev, acme-staging. Confirm? [Yn]
ERROR: command does not support multiple stacks
```

# Commands

```
$ dagger help

Usage:
  dagger [command]

Available Commands:
  help        Help about any command

  new         Create a new stack
  list        List available stacks
  query       Query the contents of a stack
  up          Bring a stack online using latest base and inputs
  down        Take a stack offline (WARNING: may destroy infrastructure)
  history     List past changes to a stack
  destroy     Destroy a stack

  base        Manage a stack's base configuration
  input       Manage a stack's inputs
  output      Manage a stack's outputs


  login       Login to Dagger Cloud
  logout      Logout from Dagger Cloud

Flags:
  -h, --help                help for dagger

  -w, --workspace           Select a workspace (default "$HOME/.dagger")
  -s, --stack               Select a stack (default: see STACK SELECTION)
  --log-format string       Log format (json, pretty). Defaults to json if the terminal is not a tty
  -l, --log-level string    Log level (default "debug")

Use "dagger [command] --help" for more information about a command.
```

### New

```
$ dagger help new
Create a new stack

Usage:
  dagger new [flags]

Flags:
  -n, --name                Specify a stack name. (default: name of current directory)
  --base-dir                Load base configuration from a local directory
  --base-git                Load base configuration from a git repository
  --base-package            Load base configuration from a cue package
  --base-file               Load base configuration from a cue or json file
  -u, --up                  Bring the stack online
  --setup no|yes|auto       Specify whether to prompt user for initial setup
```

### List

```
$ dagger help list
List available stacks

Usage:
  dagger list [flags]
```

### Query

```
$ dagger help query
Query the contents of a stack

Usage:
  dagger query [EXPR] [flags]

  EXPR may be any valid CUE expression. The expression is evaluated against the stack contents. The stack is not changed.

Examples:

  # Print all contents of the input and base layers (omit output)
  $ dagger query -l input,base

  # Print complete contents for a particular component
  $ dagger query www.build

  # Print the URL of a deployed service
  $ dagger query api.url

  # Export environment variables from a deployment
  $ dagger query -f json api.environment


Flags:
  -h, --help           help for query
  -v,--version         Stack version to query (default "latest")
  -f, --format string  Output format ("json", "yaml", "cue", "text", "env")
  -l, --layer string   Comma-separated list of layers to query (any of "input", "base", "output". default "all")
```


### Up

```
$ dagger help up
Bring a stack online using latest base and inputs

Usage:
  dagger up [flags]

Flags:
  -h, --help           help for up
  --no-cache           disable all run cache
```


### Down

```
$ dagger help down
Take a stack offline (WARNING: may destroy infrastructure)

Usage:
  dagger down [flags]

Flags:
  -h, --help           help for down
  --no-cache           disable all run cache
```

### History

```
$ dagger help history
List past changes to a stack

Usage:
  dagger history
```



### Destroy

```
$ dagger help destroy
Destroy an environment

Usage:
  dagger destroy [flags]

Flags:
  -f, --force                Destroy environment state even if cleanup pipelines fail to complete (EXPERTS ONLY)
```


### Base

```
$ dagger help base
Manage a stack's base configuration

Commands:
  package          Load base configuration from a cue package
  dir              Load base configuration from a local directory
  git              Load base configuration from a git repository
  file             Load base configuration from a cue file
```

#### Base package

```
$ dagger help base package
Load base configuration from a cue package

Usage:
  dagger base package PKG

Examples:
  dagger base package dagger.io/templates/jamstack

Flags:
  -h, --help
```

#### Base directory

```
$ dagger help base dir
Load base configuration from a local directory

Usage:
  dagger base dir PATH

Examples:
  dagger base dir ./infra/prod

Flags:
  -h, --help
```

#### Base git repository

```
$ dagger help base git
Load base configuration from a git repository

Usage:
  dagger base git REMOTE REF [SUBDIR]

Examples:
  dagger base git https://github.com/dagger/dagger main examples/simple

Flags:
  -h, --help
```


#### Base file

```
$ dagger help base file
Load base configuration from a cue or json file

Usage:
  dagger base file PATH|-

Examples:
  dagger base file ./base.cue
  echo 'message: "hello, \(name)!", name: string | *"world"' | dagger base file -

Flags:
  -h, --help
```

### Input

```
$ dagger help input
Manage a stack's inputs

Commands:
  dir              Add a local directory as input artifact
  git              Add a git repository as input artifact
  container        Add a container image as input artifact
  value            Add an input value
  secret           Add an encrypted input secret
```

FIXME: individual input commands


### Login

```
$ dagger help login
Login to Dagger Cloud

Usage:
  dagger login
```

### Logout

```
$ dagger help logout
Logout from Dagger Cloud

Usage:
  dagger logout
```

# Stack selection

Almost all dagger commands take place within an environment.

Each environment has a globally unique ID

Before executing each command, `dagger` selects which environment to execute the command in. The selection process is the following:

1. If an environment is explicitly specified with `--env` or `-e`, use that.
2. If .daggerenv exists in the current directory, use its contents.
