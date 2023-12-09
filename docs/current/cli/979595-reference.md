---
slug: /cli/979595/reference
pagination_next: null
pagination_prev: null
---

# Reference

The Dagger CLI provides a command-line interface to Dagger.

## Usage

```shell
dagger [options] [command]
```

## Options

The options below can be used with all CLI commands.

| Option                | Description                                                      |
| --------------------- | ---------------------------------------------------------------- |
| `--cpuprofile string` | Collect CPU profile to path, and trace at path.trace             |
| `--debug`             | Show more information for debugging                              |
| `--pprof string`      | Serve HTTP pprof at this address                                 |
| `--progress string`   | Progress output format (`auto`, `plain`, `tty`) (default `auto`) |
| `-s`, `--silent`      | Disable terminal UI and progress output                          |
| `-h`, `--help`        | Show help text                                                   |
| `--workdir`           | Define the host working directory (default `.`)                  |

## Commands

## dagger call

:::note
This command is currently under development, may change in future and is therefore hidden in the CLI.
:::

Call a module function and print the result. When called:

- on a container, the standard output is returned;
- on a directory, the list of entries is returned;
- on a file, the file contents are returned.

### Usage

```shell
dagger call [function]
```

### Examples

Call a function returning a container. The standard output of the container is returned.

```shell
dagger call test
```

## dagger completion

Generate the autocompletion script for dagger for the specified shell. Available shells are `bash`, `fish`, `zsh` and `powershell`.

:::note
You will need to start a new shell for this setup to take effect.
:::

### Usage

```shell
dagger completion [shell]
```

### Examples

Load completions for every new session on Linux:

```shell
dagger completion bash > /etc/bash_completion.d/dagger
```

To load completions for every new session on macOS:

```shell
dagger completion bash > $(brew --prefix)/etc/bash_completion.d/dagger
```

## dagger functions

:::note
This command is currently under development, may change in future and is therefore hidden in the CLI.
:::

List all functions in a module.

### Options

| Option               | Description                           |
| ---------------------| --------------------------------------|
| `--focus`            | Only show output for focused commands |
| `-m, --mod string` | Path to `dagger.json` config file for the module or a directory containing that file. May be a local path or a remote Git repository |

### Usage

```shell
dagger functions
```

### Examples

List functions in local module:

```shell
dagger functions -m /path/to/some/dir
```

List functions in remote module:

```shell
dagger functions -m github.com/dagger/hello-dagger
```

## dagger help

### Usage

```shell
dagger help [command]
```

Retrieve detailed help text for any command in the application.

### Example

Obtain help for the `dagger query` command:

```shell
dagger help query
```

## dagger login

Log in to Dagger Cloud.

### Usage

```shell
dagger login
```

### Example

Log in to Dagger Cloud:

```shell
dagger login
```

## dagger logout

Log out of Dagger Cloud.

### Usage

```shell
dagger logout
```

### Example

Log out of Dagger Cloud:

```shell
dagger logout
```

## dagger mod

Manage Dagger modules. By default, print the configuration of the current module in JSON format.

### Usage

```shell
dagger mod [-m string] [--focus]
dagger mod [-m string] [--focus] [sub-command [sub-command options]]
```

### Options

| Option               | Description                           |
| ---------------------| --------------------------------------|
| `--focus`            | Only show output for focused commands |
| `-m, --mod string` | Path to `dagger.json` config file for the module or a directory containing that file. May be a local path or a remote Git repository |

### Examples

Print the configuration of a local Dagger module:

```shell
dagger mod -m /path/to/some/dir
```

Print the configuration of a remote Dagger module:

```shell
dagger mod -m github.com/dagger/hello-dagger
```

### Sub-commands

| Sub-command  | Description                                                           |
| ------------ | --------------------------------------------------------------------- |
| `init`       | Initialize a new Dagger module in a local directory                   |
| `install`    | Add a new dependency to a Dagger module                              |
| `sync`       | Synchronize a Dagger module with the latest version of its extensions |
| `publish`    | Publish a Dagger module to the Daggerverse                            |

#### dagger mod init

Initialize a new Dagger module in the current directory.

##### Usage

```shell
dagger mod init --name string --sdk string [--root string] [--license string]
```

##### Options

| Option               | Description                                                     |
| ---------------------| ----------------------------------------------------------------|
| `--license string`   | License identifier to generate                                  |
| `--name string`      | Name of the new module                                          |
| `--root string`      | Root directory that should be loaded for the full module context. Defaults to the parent directory containing `dagger.json` |
| `--sdk string`       | DK name or image ref to use for the module                      |

##### Example

Initialize a new module named `hello` with the Python SDK:

```shell
dagger mod init --name=hello --sdk=python
```

#### dagger mod install

Add a new dependency to a Dagger module.

##### Usage

```shell
dagger mod install
```

##### Example

Install the `ttlsh` module from the Daggerverse:

```shell
dagger mod install github.com/shykes/daggerverse/ttlsh@16e40ec244966e55e36a13cb6e1ff8023e1e1473
```

#### dagger mod sync

Synchronize a Dagger module after a change in its function signature(s).

:::note
This is only required for IDE auto-completion/LSP purposes.
:::

##### Usage

```shell
dagger mod sync
```

#### dagger mod publish

Publish a Dagger module to the Daggerverse.

##### Usage

```shell
dagger mod publish [--force]
```

##### Options

| Option      | Description                                                     |
| ------------| ----------------------------------------------------------------|
| `-f, --force`   | Force publish even if the Git repository is not clean           |

## dagger query

Send API queries to the Dagger Engine. When no file provider, read query string from standard input.

### Usage

```shell
dagger query [--doc file] [--var string] [--var-json string] [query]
```

### Options

| Option       | Description                 |
| ------------ | --------------------------- |
| `--doc`      | Read query from file        |
| `--var`      | Read query from string      |
| `--var-json` | Read query from JSON string |

### Example

Execute an API query:

```shell
dagger query <<EOF
{
  container {
    from(address:"hello-world") {
      exec(args:["/hello"]) {
        stdout {
          contents
        }
      }
    }
  }
}
EOF
```

## dagger run

Executes the specified command in a Dagger session and displays live progress. `DAGGER_SESSION_PORT` and `DAGGER_SESSION_TOKEN` will be injected automatically.

In the live progress output:

- Parallel pipelines are represented by vertical columns (┃ for active, │ for inactive).
- Operations within a pipeline are represented by block characters (█).
- Sub-tasks of an operation are listed beneath it (┣).
- Actively running operations blink until they finish (█▓▒░█▓▒░).
- Each pipeline forks from its parent pipeline and show its name in bold after a caret (▼).
- Dependencies across pipelines are shown as a column that forks beneath the output operation and connects to each input, with the name of the output in grey (`pull docker.io/library/node:16`).
- Actively running operations are always shown at the bottom of the screen.
- Failed operations are also always shown at the bottom of the screen.

### Usage

```shell
dagger run [--debug] [--cleanup-timeout integer] [--focus] [command]
```

### Options

| Option       | Description                  |
| ------------ | -----------------------------|
| `--debug`    | Display underlying API calls |
| `--cleanup-timeout duration` |  Set max duration to wait between SIGTERM and SIGKILL on interrupt (default 10s) |
| `--focus`    | Only show output for focused commands |

### Examples

Make an HTTP request using `curl`:

```shell
dagger run -- sh -c 'curl \
  -u $DAGGER_SESSION_TOKEN: \
  -H "content-type:application/json" \
  -d "{\"query\":\"{container{id}}\"}" \
  http://127.0.0.1:$DAGGER_SESSION_PORT/query'
```

Direct command output to file, displaying progress in terminal:

```shell
dagger run go run ci.go > foo.out
```

Output Dagger logs to file:

```shell
dagger run go run ci.go > log.txt 2>&1
```

Output Dagger logs to terminal and to file:

```shell
dagger run go run ci.go 2>&1 | tee log.txt
```

Disable Dagger terminal output, but continue emitting program standard output:

```shell
dagger --silent run go run ci.go 2>&1 | tee foo.out
```

## dagger shell

:::note
This command is currently under development, may change in future and is therefore hidden in the CLI.
:::

Open a shell in a container returned by a function. If no entrypoint is specified and the container doesn't have a default command, `sh` will be used for the shell.

### Usage

```shell
dagger shell [--entrypoint strings] [function]
```

### Options

| Option                 | Description                  |
| ---------------------- | -----------------------------|
| `--entrypoint strings` | Entrypoint to use            |

### Example

Open a shell session in the container returned by the `debug` function:

```shell
dagger shell debug
```

Open a shell session in the container returned by the `debug` function using a different entrypoint:

```shell
dagger shell --entrypoint /bin/zsh debug
```

## dagger up

:::note
This command is currently under development, may change in future and is therefore hidden in the CLI.
:::

Start a service returned by a function and expose its ports to the host.

:::note
In order for this to work, the service returned by the function must have the `Container.withExposedPort` field defining one or more exposed ports.
:::

### Usage

```shell
dagger up [--port string] [--native] [function]
```

### Options

| Option        | Description                                                        |
| ------------- | -------------------------------------------------------------------|
| `-p, --port string` | Port forwarding rule in FRONTEND[:BACKEND][/PROTO] format          |
| `-n, --native`    | Forward all ports natively, matching frontend port to backend port |

### Examples

Start the service returned by the `service` function:

```shell
dagger up --native service
```

Start the service returned by the `service` function, mapping container port 8080 to host port 9090:

```shell
dagger up --port 9090:8080 service
```

## dagger version

Display version.

### Usage

```shell
dagger version
```

### Example

Display the current version:

```shell
dagger version
```
