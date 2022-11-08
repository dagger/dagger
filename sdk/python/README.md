# Dagger Python SDK

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
        # note: this is reusable, no request is made to the server
        ctr = (
            client.container()
            .from_("python:alpine")
            .exec(["pip", "install", "cowsay"])
            .with_entrypoint(["cowsay"])
        )

        # run cowsay with requested message
        # note: methods that return a coroutine with a Result need to
        # await query execution
        result = await ctr.exec(args).stdout().contents()

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

- [Documentation](https://docs.dagger.io)
- [Source code](https://github.com/dagger/dagger/tree/main/sdk/python)

## Development

Requirements:

- Python 3.10+
- [Hatch](https://hatch.pypa.io/latest/install/)
- [Docker](https://docs.docker.com/engine/install/)

Run tests with `hatch run test`.

Run the linter, reformatting code with `hatch run lint:fmt` or just check with `hatch run lint:style`.

Re-regenerate client with `hatch run generate`. Remember to run `hatch run lint:fmt` afterwards for consistent output!
