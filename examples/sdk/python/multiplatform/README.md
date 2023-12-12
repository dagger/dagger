# Multiplatform

This example builds go binaries for two architectures (`amd64` and `arm64`) and exports the binaries to the current directory.
```
linux
├── amd64
│   └── dagger
└── arm64
    └── dagger
```

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
