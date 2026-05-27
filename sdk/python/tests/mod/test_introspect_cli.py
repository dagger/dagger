"""Smoke test for the `python -m dagger.mod._introspect emit` CLI."""

from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path

_MODULE_SRC = """\
import dagger


@dagger.object_type
class Foo:
    @dagger.function
    def echo(self, msg: str) -> str:
        return msg
"""


def test_emit_cli_writes_module_types_json(tmp_path: Path):
    pkg = tmp_path / "mypkg"
    pkg.mkdir()
    (pkg / "__init__.py").write_text(_MODULE_SRC, encoding="utf-8")

    out = tmp_path / "module-types.json"
    env = {
        **os.environ,
        "DAGGER_DEFAULT_PYTHON_PACKAGE": "mypkg",
        "DAGGER_MODULE": "foo",
        "DAGGER_MAIN_OBJECT": "Foo",
        "PYTHONPATH": str(tmp_path),
    }

    result = subprocess.run(
        [sys.executable, "-m", "dagger.mod._introspect", "emit", "--output", str(out)],
        env=env,
        capture_output=True,
        text=True,
        check=False,
    )
    assert result.returncode == 0, result.stderr

    data = json.loads(out.read_text(encoding="utf-8"))
    names = {t["name"] for t in data["__schema"]["types"]}
    assert {"Foo", "Query"} <= names
    foo = next(t for t in data["__schema"]["types"] if t["name"] == "Foo")
    assert any(f["name"] == "echo" for f in foo["fields"])
