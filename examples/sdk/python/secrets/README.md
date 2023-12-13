# Secrets example

This example shows how Dagger Secrets can be created and used in a pipeline. It also demonstrates how Secrets will not leak their contents in output unless you explicitly convert them to plaintext first.

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
