
# Commands


```
$ dagger help

A reactive automation platform

Usage:
  dagger [command]

Available Commands:
  help        Help about any command
  info        Show contextual information

  init        Initialize an environment
  destroy     Destroy an environment
  change      Make a change to an environment

  query       Query the state of an environment
  history     List past changes to an environment
  download    Download data from an environment

  sync        Synchronize local state with Dagger Cloud
  login       Login to Dagger Cloud
  logout      Logout from Dagger Cloud

Flags:
  -h, --help                help for dagger

  -w, --workspace           Select a workspace (default "$HOME/.dagger")
  -e, --env                 Select an environment (default: see ENVIRONMENT SELECTION)

Use "dagger [command] --help" for more information about a command.
```

## Info


```
$ dagger help info
Show contextual information

Usage:
  dagger info
```

### Init

```
$ dagger help init
Initialize an environment

Usage:
  dagger init [flags]

Flags:
  -n, --name                Specify the new environment's name. (default: name of current directory)
  --setup no|yes|auto       Specify whether to prompt user for initial setup
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


### Change

```
$ dagger help change
Make a change to an environment

Usage:
  dagger change [flags]

Examples:

  $ dagger change -i
  [...] rough approximation of an interactive terminal wizard below
  [www.domain] Netlify domain: acme.infralabs.io
  [www.token] Netlify API token: ************
  [api.auth.accessKey] AWS Access Key: ***********
  [api.auth.secretKey] AWS Secret Key: ***********
  [api.source] Source code: [directory...]

  # Migrate website to a new domain
  $ dagger change --input-string www.domain=acme.infralabs.io

  # Migrate to a new infrastructure stack
  $ dagger change --base infrav2

  # Deploy a new app version
  $ dagger change --input-dir www.source=./frontend --input-git api.source=https://github.com/foo/bar#main

  # Rollback to 2 versions ago
  $ dagger change --rollback -- -2

Flags:
  -i, --interactive            Interactively prompt for change information 
  -n, --name NAME              Change the environment name
  -b, --base ENV | PKG         Change the environment base configuration. May be another environment or a cue package.

  --stdlib VERSION             Select stdlib version (default "latest")

  --input-string KEY=VALUE     Add a string value to the environment input
  --input-dir KEY=PATH         Add a directory to the environment input
  --input-secret KEY[=PATH]    Add an encrypted secret to the environment input
  --input-json KEY=JSON        Add a json value to the environment input
  --input-git KEY=REMOTE#REF   Add a git repository to the environment input

  --rollback [VERSION]         Roll back the environment state to a previous version.

  --log-format string          Log format (json, pretty). Defaults to json if the terminal is not a tty
  -l, --log-level string       Log level (default "debug")
```

### Query

```
$ dagger help query
Query the state of an environment

Usage:
  dagger query [EXPR] [flags]

  EXPR may be any valid CUE expression. The expression is evaluated against the environment state. The environment state is not changed.

Examples:

  # Print all input values
  $ dagger query --input

  # Print complete state for a particular component
  $ dagger query www.build

  # Export environment variables from a deployment
  $ dagger query -o json api.environment


Flags:
  -h, --help           help for query
  -v,--version         Environment version to query (default "latest")
  -o, --out string      Output format ("json", "yaml", "cue", "text", "envfile"). Default "cue".
```


### History

```
$ dagger help history
List past changes to an environment

Usage:
  dagger history
```


### Download

```
$ dagger help download
Download data from an environment

Usage:
  dagger download [KEY...] [flags]

Flags:
  -a,--all            Download all available data in the environment
  -d DIR              Target directory (default "dagger-download")
```

### Cloud commands

```
$ dagger help sync
Synchronize local state with Dagger Cloud

Usage:
  dagger sync [flags]
```

```
$ dagger help login
Login to Dagger Cloud

Usage:
  dagger login
```

```
$ dagger help logout
Logout from Dagger Cloud

Usage:
  dagger logout
```

# Environment selection

Almost all dagger commands take place within an environment.

Each environment has a globally unique ID

Before executing each command, `dagger` selects which environment to execute the command in. The selection process is the following:

1. If an environment is explicitly specified with `--env` or `-e`, use that.
2. If .daggerenv exists in the current directory, use its contents.
