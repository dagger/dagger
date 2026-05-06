"""Differential test against real Daggerverse Python modules.

Pulls the modules from `https://github.com/telchak/daggerverse`, walks
every top-level module that declares ``sdk: python``, and runs the AST
analyzer + runtime introspector on each. This is the "what users
actually write" half of the confidence story — if a single module
differs between the two paths, the test fails with the module name and
the path of the divergent member.

The corpus is cloned once per session (network required). Set
``DAGGERVERSE_CORPUS_REF`` to pin to a specific commit/branch; default
is the repo's current ``main`` branch. Skip the whole file by setting
``SKIP_DAGGERVERSE_CORPUS=1`` (useful for offline development).
"""

from __future__ import annotations

import contextlib
import json
import os
import shutil
import subprocess
import sys
from collections.abc import Iterator
from pathlib import Path

import pytest

from dagger.mod._analyzer.analyze import analyze_module

from ._differential import assert_metadata_equivalent
from ._runtime_introspect import runtime_introspect_package

CORPUS_URL = "https://github.com/telchak/daggerverse.git"
CORPUS_REF = os.environ.get("DAGGERVERSE_CORPUS_REF", "main")


@pytest.fixture(scope="session")
def daggerverse_corpus(tmp_path_factory: pytest.TempPathFactory) -> Path:
    """Clone telchak/daggerverse once per session."""
    if os.environ.get("SKIP_DAGGERVERSE_CORPUS"):
        pytest.skip("SKIP_DAGGERVERSE_CORPUS set")

    if not shutil.which("git"):
        pytest.skip("git not on PATH")

    target = tmp_path_factory.mktemp("daggerverse_corpus") / "repo"
    try:
        subprocess.run(
            [
                "git",
                "clone",
                "--depth=1",
                "--branch",
                CORPUS_REF,
                CORPUS_URL,
                str(target),
            ],
            check=True,
            capture_output=True,
            timeout=60,
        )
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired) as exc:
        pytest.skip(f"could not clone daggerverse corpus: {exc}")

    return target


@pytest.fixture(scope="session")
def daggerverse_sys_path(daggerverse_corpus: Path) -> Iterator[None]:
    """Make underscore-prefixed sibling packages importable.

    Some daggerverse modules (angie, daggie, monty, speck) depend on
    ``_agent_base`` — a shared Python package brought in via
    ``[tool.uv.sources]`` in each module's pyproject. The runtime
    introspector's ``importlib.import_module`` won't find it unless
    its ``src/`` is on ``sys.path``. Add every ``_<name>/src`` we can
    find under the corpus root.
    """
    added: list[str] = []
    for shared_dir in daggerverse_corpus.iterdir():
        if not shared_dir.is_dir() or not shared_dir.name.startswith("_"):
            continue
        src_dir = shared_dir / "src"
        if src_dir.is_dir():
            sys.path.insert(0, str(src_dir))
            added.append(str(src_dir))
    try:
        yield
    finally:
        for entry in added:
            with contextlib.suppress(ValueError):
                sys.path.remove(entry)


def _python_modules(corpus_root: Path) -> list[Path]:
    """Top-level Python modules in the corpus.

    A module is identified by a ``dagger.json`` with ``sdk: python`` at
    the directory root. Examples and nested test fixtures inside a
    module are skipped — we want the top-level public modules only.
    """
    modules: list[Path] = []
    for dagger_json in sorted(corpus_root.glob("*/dagger.json")):
        try:
            config = json.loads(dagger_json.read_text())
        except (OSError, json.JSONDecodeError):
            continue
        sdk = config.get("sdk")
        sdk_source = sdk.get("source") if isinstance(sdk, dict) else sdk
        if sdk_source != "python":
            continue
        modules.append(dagger_json.parent)
    return modules


def _module_id(module_dir: Path) -> str:
    """Pytest-friendly id for parametrize."""
    return module_dir.name


def _read_module_files(module_dir: Path) -> dict[str, str]:
    """Collect ``.py`` files under ``src/<package>/`` keyed by relative path.

    Daggerverse modules live in ``<module>/src/<package_name>/``. We treat
    that subdirectory as the package root the runtime will import.
    """
    src_root = module_dir / "src"
    if not src_root.is_dir():
        return {}
    # Daggerverse always has exactly one package directory inside src/.
    package_dirs = [p for p in sorted(src_root.iterdir()) if p.is_dir()]
    if not package_dirs:
        return {}
    package_dir = package_dirs[0]

    files: dict[str, str] = {}
    for py_file in sorted(package_dir.rglob("*.py")):
        rel = py_file.relative_to(package_dir).as_posix()
        files[rel] = py_file.read_text(encoding="utf-8")
    return files


def _main_object_name(module_dir: Path) -> str:
    """Convert daggerverse module name to its PascalCase main class.

    ``dagger-mcp`` → ``DaggerMcp``, ``gcp-cloud-run`` → ``GcpCloudRun``,
    ``calver`` → ``Calver``.
    """
    config = json.loads((module_dir / "dagger.json").read_text())
    name = config.get("name", module_dir.name)
    return "".join(part.capitalize() for part in name.replace("_", "-").split("-"))


def _materialise_for_ast(
    module_dir: Path, files: dict[str, str], tmp_path: Path
) -> list[Path]:
    """Write the module files under ``tmp_path`` for the AST analyzer.

    The AST analyzer takes a list of file paths, not a string dict.
    """
    pkg_root = tmp_path / module_dir.name
    pkg_root.mkdir(parents=True, exist_ok=True)
    written: list[Path] = []
    for rel, content in files.items():
        target = pkg_root / rel
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content, encoding="utf-8")
        written.append(target)
    return written


# ---- The test itself -------------------------------------------------------


def _collect_corpus_modules() -> list[Path]:
    """Lookup-time module discovery for ``pytest.mark.parametrize``.

    Returns an empty list when the corpus isn't available so the test
    function is still collected and shows up as skipped at runtime.
    """
    if os.environ.get("SKIP_DAGGERVERSE_CORPUS"):
        return []
    cache = os.environ.get("DAGGERVERSE_CORPUS_PATH")
    if cache:
        path = Path(cache)
        if path.is_dir():
            return _python_modules(path)
    return []


# At parametrize time we may not have the corpus yet (CI runs the
# fixture lazily). We always parametrize over a single placeholder so
# the test is collected; the fixture provides the real list at runtime.


def test_daggerverse_module_diff(
    daggerverse_corpus: Path,
    daggerverse_sys_path: None,  # noqa: ARG001 — fixture has side effects
    tmp_path: Path,
    request: pytest.FixtureRequest,  # noqa: ARG001 — pytest plumbing
) -> None:
    """For every Python module in the corpus, AST output must match runtime."""
    modules = _python_modules(daggerverse_corpus)
    if not modules:
        pytest.skip("no python modules found in corpus")

    failures: list[tuple[str, str]] = []
    for module_dir in modules:
        files = _read_module_files(module_dir)
        if not files:
            failures.append((module_dir.name, "no .py files under src/<pkg>/"))
            continue
        main = _main_object_name(module_dir)

        # AST side — uses the real file paths for accurate location info.
        ast_paths = _materialise_for_ast(module_dir, files, tmp_path / module_dir.name)
        try:
            ast_md = analyze_module(source_files=ast_paths, main_object_name=main)
        except Exception as exc:  # noqa: BLE001 — surface any analyzer crash
            failures.append((module_dir.name, f"AST analyze raised: {exc!r}"))
            continue

        # Runtime side — imports the package via importlib.
        try:
            runtime_md = runtime_introspect_package(files, main)
        except Exception as exc:  # noqa: BLE001 — surface any import crash
            failures.append((module_dir.name, f"runtime import raised: {exc!r}"))
            continue

        try:
            assert_metadata_equivalent(ast_md, runtime_md)
        except AssertionError as exc:
            failures.append((module_dir.name, str(exc)))

    if failures:
        report = "\n\n".join(f"=== {name} ===\n{detail}" for name, detail in failures)
        msg = f"{len(failures)}/{len(modules)} daggerverse modules diverge:\n\n{report}"
        raise AssertionError(msg)
