"""Unit tests for static-entrypoint detection in the def phase.

The def phase prefers a committed ``<pkg>/_dagger_main.py`` (zero analysis)
and otherwise falls back to importing + introspecting the live module. These
cover the pure detection seam; the engine replay itself is integration-tested.
"""

from __future__ import annotations

from dagger.mod._module import _import_static_entrypoint


def _write_pkg(tmp_path, pkg: str, *, with_entrypoint: bool) -> None:
    pkgdir = tmp_path / pkg
    pkgdir.mkdir()
    (pkgdir / "__init__.py").write_text("")
    if with_entrypoint:
        (pkgdir / "_dagger_main.py").write_text(
            "async def typedefs():\n    return 'sentinel'\n"
        )


def test_returns_entrypoint_module_when_present(tmp_path, monkeypatch):
    _write_pkg(tmp_path, "withep", with_entrypoint=True)
    monkeypatch.syspath_prepend(str(tmp_path))

    mod = _import_static_entrypoint("withep")

    assert mod is not None
    assert hasattr(mod, "typedefs")


def test_returns_none_when_package_has_no_entrypoint(tmp_path, monkeypatch):
    _write_pkg(tmp_path, "noep", with_entrypoint=False)
    monkeypatch.syspath_prepend(str(tmp_path))

    assert _import_static_entrypoint("noep") is None


def test_returns_none_when_package_missing(tmp_path, monkeypatch):
    monkeypatch.syspath_prepend(str(tmp_path))

    assert _import_static_entrypoint("doesnotexist") is None
