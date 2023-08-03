# Dagger Python SDK

[![PyPI Version](https://img.shields.io/pypi/v/dagger-io)](https://pypi.org/project/dagger-io/)
[![Conda Version](https://img.shields.io/conda/vn/conda-forge/dagger-io.svg)](https://anaconda.org/conda-forge/dagger-io)
[![Supported Python Versions](https://img.shields.io/pypi/pyversions/dagger-io.svg)](https://pypi.org/project/dagger-io/)
[![License](https://img.shields.io/pypi/l/dagger-io.svg)](https://pypi.python.org/pypi/dagger-io)
[![Code style](https://img.shields.io/badge/code%20style-black-black.svg)](https://github.com/psf/black)
[![Ruff](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/charliermarsh/ruff/main/assets/badge/v1.json)](https://github.com/charliermarsh/ruff)

A client package for running [Dagger](https://dagger.io/) pipelines.

## What is the Dagger Python SDK?

The Dagger Python SDK contains everything you need to develop CI/CD pipelines in Python, and run them on any OCI-compatible container runtime.

## Requirements

- Python 3.10 or later
- [Docker](https://docs.docker.com/engine/install/), or another OCI-compatible container runtime

A compatible version of the  [Dagger CLI](https://docs.dagger.io/cli) is automatically downloaded and run by the SDK for you, although it’s possible to manage it manually.

## Installation

From [PyPI](https://pypi.org/project/dagger-io/), using `pip`:

```shell
pip install dagger-io
```

You can also install via [Conda](https://anaconda.org/conda-forge/dagger-io), from the [conda-forge](https://conda-forge.org/docs/user/introduction.html#how-can-i-install-packages-from-conda-forge) channel:

```shell
conda install dagger-io
```

## Example

Create a `main.py` file:

```python
import sys

import anyio
import dagger


async def main(args: list[str]):
    async with dagger.Connection() as client:
        # build container with cowsay entrypoint
        ctr = (
            client.container()
            .from_("python:alpine")
            .with_exec(["pip", "install", "cowsay"])
            .with_entrypoint(["cowsay"])
        )

        # run cowsay with requested message
        result = await ctr.with_exec(args).stdout()

    print(result)


anyio.run(main, sys.argv[1:])
```

Run with:

```console
$ python main.py "Simple is better than complex"
  _____________________________
| Simple is better than complex |
  =============================
                             \
                              \
                                ^__^
                                (oo)\_______
                                (__)\       )\/\
                                    ||----w |
                                    ||     ||
```

> **Note**
> It may take a while for it to finish, especially on first run with cold cache.

If you need to debug, you can stream the logs from the engine with the `log_output`  config:

```python
config = dagger.Config(log_output=sys.stderr)
async with dagger.Connection(config) as client:
    ...
```

## Learn more

- [Documentation](https://docs.dagger.io/sdk/python)
- [API Reference](https://dagger-io.readthedocs.org)
- [Source code](https://github.com/dagger/dagger/tree/main/sdk/python)

## Development

This library is maintained with [Hatch](https://hatch.pypa.io/).

The following commands are available:
- `hatch run test`: Run tests.
- `hatch run fmt`: Re-format code following common styling conventions.
- `hatch run lint`: Check for linting violations.
- `hatch run typing:check`: Run the type checker.
- `hatch run docs:build`: Build reference docs locally
- `hatch run docs:preview`: Build and serve reference docs (defaults to localhost:8000)

### Engine changes

Testing and regenerating the client may fail if there’s changes in the engine code that haven’t been released yet.

The simplest way to run those commands locally with the most updated engine version is to build it using [Dagger’s CI pipelines](https://github.com/dagger/dagger/blob/main/internal/mage/sdk/python.go) :

```shell
../../hack/make sdk:python:generate
../../hack/make sdk:python:lint
../../hack/make sdk:python:test
```

You can also build the CLI and use it directly within the Python SDK:

```shell
../../hack/dev hatch test
```

Or build it separately and tell the SDK to use it directly (or any other CLI binary):

```shell
../../hack/make
_EXPERIMENTAL_DAGGER_CLI_BIN=../../bin/dagger hatch test
```

