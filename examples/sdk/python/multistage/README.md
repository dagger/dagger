# Multistage build

This example performs a multistage build and then pushes a container image to a registry, in this case ttl.sh.

Ideally, set up a Virtual Env with the Dagger Python SDK to run in:
```
python3 -m venv .venv
source .venv/bin/activate
pip install -U pip dagger-io
```

Then to run the example:
```
dagger run python pipeline.py
```
