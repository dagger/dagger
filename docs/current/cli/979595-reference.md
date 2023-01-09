---
slug: /cli/979595/reference
---

# Reference

The Dagger CLI provides a command-line interface to Dagger.

## Usage

```shell
dagger [options] [command]
```

## Options

The options below can be used with all CLI commands.

| Option         | Description                                     |
| -------------- | ----------------------------------------------- |
| `--debug`      | Show Buildkitd debug logs                       |
| `-h`, `--help` | Show help text                                  |
| `--workdir`    | Define the host working directory (default `.`) |
| ---            | ---                                             |

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

## dagger run

Executes the specified command in a Dagger session. `DAGGER_SESSION_PORT` and `DAGGER_SESSION_TOKEN` will be injected automatically.

### Usage

```shell
dagger run [command]
```

### Example

Make an HTTP request using `curl`:

```shell
dagger run -- sh -c 'curl \
  -u $DAGGER_SESSION_TOKEN: \
  -H "content-type:application/json" \
  -d "{\"query\":\"{container{id}}\"}" \
  http://127.0.0.1:$DAGGER_SESSION_PORT/query'
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
| ---          | ---                         |

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
