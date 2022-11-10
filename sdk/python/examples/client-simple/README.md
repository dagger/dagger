# Simple example using the Python SDK client

## Requirements

- Python 3.10+
- [Docker](https://docs.docker.com/engine/install/)

## How to use?

```sh
python3 -m venv .venv
source .venv/bin/activate
pip install --pre dagger-io
python say.py "Simple is better than complex"
```

## Note on logs

The examples, by default, don't stream output from the engine so it may seem like
it's hanging especially on first run. Notice the comment above establishing the
connection, we show how you can stream those logs to your screen.

It's not doing that by default just to keep the output from `cowsay` clean.

## More

There's more examples at [helderco/dagger-examples](https://github.com/helderco/dagger-examples).
