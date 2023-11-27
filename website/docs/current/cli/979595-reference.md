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
| --------------------- | ---------------------------------------------------------------- |

## Commands

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

* Parallel pipelines are represented by vertical columns (┃ for active, │ for inactive).
* Operations within a pipeline are represented by block characters (█).
* Sub-tasks of an operation are listed beneath it (┣).
* Actively running operations blink until they finish (█▓▒░█▓▒░).
* Each pipeline forks from its parent pipeline and show its name in bold after a caret (▼).
* Dependencies across pipelines are shown as a column that forks beneath the output operation and connects to each input, with the name of the output in grey (`pull docker.io/library/node:16`).
* Actively running operations are always shown at the bottom of the screen.
* Failed operations are also always shown at the bottom of the screen.

### Usage

```shell
dagger run [--debug] [command]
```

### Options

| Option       | Description                  |
| ------------ | -----------------------------|
| `--debug`    | Display underlying API calls |

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
