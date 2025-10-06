"""A pre v0.13 Python module for testing the pip based requirements.lock file"""

import os
from importlib import metadata

import dagger


@dagger.object_type
class Test:
    @dagger.function
    def versions(self, names: list[str]) -> list[str]:
        return [f"{name}=={metadata.version(name)}" for name in names]

    @dagger.function
    def uv_version(self) -> str:
        return os.getenv("UV_VERSION", "🤷")
