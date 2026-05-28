"""Module discovery helpers.

The import package name for the user's module, used to locate the main object
entry point and the committed static entrypoint (``<pkg>/_dagger_main.py``).
"""

from __future__ import annotations

import os
import typing

IMPORT_PKG: typing.Final[str] = os.getenv("DAGGER_DEFAULT_PYTHON_PACKAGE", "main")
