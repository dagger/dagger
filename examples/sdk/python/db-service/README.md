# Database Service in a Pipeline

This example shows the usage of a postgres database used in the integration tests for an application. The pipeline uses a postgres container as a service alongside the testing pipeline.

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

This will start a dagger pipeline to run the tests in `test_pipeline.py`
