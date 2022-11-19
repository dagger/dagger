# Dagger Python SDK

[![PyPI](https://img.shields.io/pypi/v/dagger-io)](https://pypi.org/project/pytest/) [![Supported Python Versions](https://img.shields.io/pypi/pyversions/dagger-io.svg)](https://pypi.org/project/pytest/) [![Code style](https://img.shields.io/badge/code%20style-black-black.svg)](https://github.com/psf/black)

A client package for running [Dagger](https://dagger.io/) pipelines.

## What is the Dagger Python SDK?

The Dagger Python SDK contains everything you need to develop CI/CD pipelines in Python, and run them on any OCI-compatible container runtime.

## Example

```python
# say.py
import sys

import anyio
import dagger


async def main(args: list[str]):
    async with dagger.Connection() as client:
        # build container with cowsay entrypoint
        ctr = (
            client.container()
            .from_("python:alpine")
            .exec(["pip", "install", "cowsay"])
            .with_entrypoint(["cowsay"])
        )

        # run cowsay with requested message
        result = await ctr.exec(args).stdout()

        print(result)


if __name__ == "__main__":
    anyio.run(main, sys.argv[1:])
```

Run with:

```console
$ python say.py "Simple is better than complex"
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

## Learn more

- [Documentation](https://docs.dagger.io/sdk/python)
- [API Reference](https://dagger-io.readthedocs.org)
- [Source code](https://github.com/dagger/dagger/tree/main/sdk/python)

## Development

Requirements:

- Python 3.10+
- [Poetry](https://python-poetry.org/docs/)
- [Docker](https://docs.docker.com/engine/install/)

Start environment with `poetry install`.

Run tests with `poetry run poe test`.

Reformat code with `poetry run poe fmt` or just check with `poetry run poe lint`.

Re-regenerate client with `poetry run poe generate`.

Build reference docs with `poetry run poe docs`.

Tip: You don't need to prefix the previous commands with `poetry run` if you activate the virtualenv with `poetry shell`.
