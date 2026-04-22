"""Tests for module discovery and AST registration integration."""

from __future__ import annotations

import importlib
import textwrap

from dagger.mod import _discovery
from dagger.mod._analyzer import analyze_module


def _write(path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(textwrap.dedent(content), encoding="utf-8")


def test_nested_packages_are_discovered(tmp_path, monkeypatch):
    pkg = tmp_path / "samplepkg"
    _write(pkg / "__init__.py", '"""Root package doc."""\n')
    _write(
        pkg / "root.py",
        """
        import dagger

        @dagger.object_type
        class Root:
            pass
        """,
    )
    _write(pkg / "features" / "__init__.py", '"""Nested doc."""\n')
    _write(
        pkg / "features" / "nested" / "service.py",
        """
        import dagger

        @dagger.object_type
        class Nested:
            pass
        """,
    )

    monkeypatch.syspath_prepend(str(tmp_path))
    monkeypatch.setenv("DAGGER_DEFAULT_PYTHON_PACKAGE", "samplepkg")
    monkeypatch.setattr(_discovery, "IMPORT_PKG", "samplepkg", raising=False)
    importlib.invalidate_caches()

    source_files = _discovery.find_source_files()
    metadata = analyze_module(
        source_files=source_files,
        main_object_name="Root",
        module_name="samplepkg",
    )

    assert "Nested" in metadata.objects
    assert metadata.doc == "Root package doc."
