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
from dagger import dag


async def main(args: list[str]):
    if not args:
        print("Error: Please provide a message to display")
        print("Example: python main.py 'Hello, World!'")
        sys.exit(1)

    try:
        async with dagger.connection():
            ctr = (
                dag.container()
                .from_("python:alpine")
                .with_exec(["pip", "install", "cowsay"])
                .with_entrypoint(["cowsay"])
            )

            result = await ctr.with_exec(args).stdout()

        print(result)
    except dagger.ExecError as e:
        print(f"Error: Command failed with exit code {e.exit_code}")
        print(f"Details: {e.stderr}")
        sys.exit(1)


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
async with dagger.connection(config):
    ...
```

## Learn more

- [Documentation](https://docs.dagger.io/sdk/python)
- [API Reference](https://dagger-io.readthedocs.org)
- [Source code](https://github.com/dagger/dagger/tree/main/sdk/python)

## Development

The SDK is managed with a Dagger module in `./dev`. To see which tasks are
available run:

```shell
dagger call -m dev
```

### Common tasks

Run pytest in supported Python versions:

```shell
dagger call -m dev test default
```

Check for linting violations:
```shell
dagger call -m dev lint
```

Re-format code following common styling conventions:
```shell
dagger call -m dev format export --path=.
```

Update pinned development dependencies:
```shell
uv lock -U
```

Build and preview the reference documentation:
```shell
dagger call -m dev docs preview up
