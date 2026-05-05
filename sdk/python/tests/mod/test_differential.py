"""Differential tests: AST analyzer output vs runtime introspection.

The AST analyzer's correctness invariant is "what
``typing.get_type_hints`` plus ``inspect`` would have computed at
runtime." This file runs the AST analyzer and a runtime introspector
on the same source, then asserts the resulting ``ModuleMetadata``
match structurally.

Each fixture stands as a single test. New patterns the analyzer
should handle should land here as a fixture — the comparator catches
silent drift between the two paths automatically. Patterns the
analyzer intentionally rejects (Literal, dict, …) belong in
``test_ast_analyzer.py`` instead, because they have no runtime
counterpart to diff against.
"""

from __future__ import annotations

from pathlib import Path

import pytest

from dagger.mod._analyzer.analyze import analyze_module, analyze_source_string

from ._differential import assert_metadata_equivalent
from ._runtime_introspect import runtime_introspect, runtime_introspect_package


def _both(source: str, main: str = "Foo", **kwargs):
    """Run both analyzers and return ``(ast_metadata, runtime_metadata)``."""
    ast_md = analyze_source_string(source, main)
    runtime_md = runtime_introspect(source, main)
    return ast_md, runtime_md


def _both_pkg(files: dict[str, str], main: str, *, tmp_path: Path):
    """Multi-file variant of ``_both``.

    Materialises ``files`` under ``tmp_path``, runs ``analyze_module`` on
    every ``.py`` file, then runs ``runtime_introspect_package`` against
    the same tree imported as a real Python package.
    """
    pkg_dir = tmp_path / "ast_pkg"
    pkg_dir.mkdir()
    paths: list[Path] = []
    for rel, content in files.items():
        target = pkg_dir / rel
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content, encoding="utf-8")
        if rel.endswith(".py"):
            paths.append(target)
    if not any(rel == "__init__.py" for rel in files):
        init = pkg_dir / "__init__.py"
        init.write_text("", encoding="utf-8")
        paths.append(init)
    ast_md = analyze_module(source_files=paths, main_object_name=main)
    runtime_md = runtime_introspect_package(files, main)
    return ast_md, runtime_md


# ---------------------------------------------------------------------------
# Trivial baseline
# ---------------------------------------------------------------------------


def test_diff_basic_object_with_field_and_function():
    source = """
import dagger

@dagger.object_type
class Foo:
    name: str = dagger.field(default="alice")

    @dagger.function
    def hello(self, msg: str = "world") -> str:
        return self.name + msg
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_async_function():
    source = """
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    async def hello(self) -> str:
        return "ok"
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


# ---------------------------------------------------------------------------
# Aliased dagger imports — the gap fixed by the alias-map work.
# ---------------------------------------------------------------------------


def test_diff_aliased_dagger_module():
    source = """
import dagger as d

@d.object_type
class Foo:
    name: str = d.field(default="x")

    @d.function
    def hello(self) -> str:
        return self.name
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_aliased_decorator_from_import():
    source = """
from dagger import object_type as ot, function as fn, field as fld

@ot
class Foo:
    name: str = fld(default="x")

    @fn
    def hello(self) -> str:
        return self.name
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


# ---------------------------------------------------------------------------
# Type aliases — module-level, PEP 695, chained.
# ---------------------------------------------------------------------------


def test_diff_module_level_type_alias():
    source = """
import dagger
from typing import Annotated

Source = Annotated[dagger.Directory, dagger.DefaultPath(".")]

@dagger.object_type
class Foo:
    @dagger.function
    def build(self, src: Source) -> dagger.Container: ...
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_pep695_type_alias():
    source = """
import dagger
from typing import Annotated

type Source = Annotated[dagger.Directory, dagger.DefaultPath(".")]

@dagger.object_type
class Foo:
    @dagger.function
    def build(self, src: Source) -> dagger.Container: ...
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_chained_type_alias():
    source = """
import dagger

A = dagger.Directory
B = A

@dagger.object_type
class Foo:
    @dagger.function
    def build(self, src: B) -> dagger.Container: ...
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


# ---------------------------------------------------------------------------
# Optional / Annotated / metadata recursion.
# ---------------------------------------------------------------------------


def test_diff_optional_annotated_metadata():
    source = """
import dagger
from typing import Optional, Annotated

@dagger.object_type
class Foo:
    @dagger.function
    def build(
        self,
        src: Optional[Annotated[dagger.Directory, dagger.DefaultPath(".")]] = None,
    ) -> dagger.Container:
        ...
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_pep604_annotated_or_none():
    source = """
import dagger
from typing import Annotated

@dagger.object_type
class Foo:
    @dagger.function
    def build(
        self,
        src: Annotated[dagger.Directory, dagger.DefaultPath(".")] | None = None,
    ) -> dagger.Container:
        ...
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


# ---------------------------------------------------------------------------
# Inheritance — fields and functions.
# ---------------------------------------------------------------------------


def test_diff_inherited_field_and_function():
    """Fields and functions inherit when Base is also @object_type."""
    source = """
import dagger
from typing import Self

@dagger.object_type
class Base:
    name: str = dagger.field(default="from-base")

    @dagger.function
    def with_name(self, n: str) -> Self:
        self.name = n
        return self

@dagger.object_type
class Foo(Base):
    @dagger.function
    def greet(self) -> str:
        return "hello " + self.name
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_inherited_function_only_from_undecorated_base():
    """An undecorated Base contributes its @function methods (Python MRO)
    but not its dagger.field() declarations (dataclass MRO requires a
    dataclass base). The differential test confirms both sides agree
    on this distinction.
    """
    source = """
import dagger
from typing import Self

class Base:
    @dagger.function
    def with_context(self) -> Self:
        return self

@dagger.object_type
class Foo(Base):
    @dagger.function
    def greet(self) -> str:
        return "hi"
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_inherited_field_overrides():
    source = """
import dagger

@dagger.object_type
class Base:
    name: str = dagger.field(default="base")

@dagger.object_type
class Foo(Base):
    name: str = dagger.field(default="foo")

    @dagger.function
    def hello(self) -> str:
        return self.name
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


# ---------------------------------------------------------------------------
# @staticmethod
# ---------------------------------------------------------------------------


def test_diff_staticmethod_first_param_kept():
    source = """
import dagger

@dagger.object_type
class Foo:
    @staticmethod
    @dagger.function
    def shout(msg: str) -> str:
        return msg.upper()
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


# ---------------------------------------------------------------------------
# Self / forward references
# ---------------------------------------------------------------------------


def test_diff_self_return_type():
    source = """
import dagger
from typing import Self

@dagger.object_type
class Foo:
    name: str = dagger.field(default="x")

    @dagger.function
    def with_name(self, name: str) -> Self:
        self.name = name
        return self
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_string_forward_reference():
    source = """
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    def me(self) -> "Foo":
        return self
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


# ---------------------------------------------------------------------------
# Lists, optional element types
# ---------------------------------------------------------------------------


def test_diff_list_of_directory():
    source = """
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    def list_dirs(self, dirs: list[dagger.Directory]) -> int:
        return len(dirs)
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_optional_list_of_strings():
    source = """
import dagger
from typing import Optional

@dagger.object_type
class Foo:
    @dagger.function
    def hello(self, items: Optional[list[str]] = None) -> int:
        return 0 if items is None else len(items)
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


# ---------------------------------------------------------------------------
# Enum
# ---------------------------------------------------------------------------


def test_diff_intenum_with_explicit_values():
    source = """
import dagger
from enum import IntEnum

@dagger.enum_type
class Priority(IntEnum):
    LOW = 1
    HIGH = 100

@dagger.object_type
class Foo:
    @dagger.function
    def get(self) -> Priority:
        return Priority.HIGH
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


# ---------------------------------------------------------------------------
# Decorator metadata: name override, doc, deprecated.
# ---------------------------------------------------------------------------


def test_diff_function_name_override():
    source = """
import dagger

@dagger.object_type
class Foo:
    @dagger.function(name="echoMessage")
    def echo(self, msg: str) -> str:
        return msg
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_field_name_override():
    source = """
import dagger

@dagger.object_type
class Foo:
    from_: str = dagger.field(name="from")

    @dagger.function
    def hello(self) -> str:
        return self.from_
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


# ---------------------------------------------------------------------------
# Documented gaps — patterns where runtime and AST intentionally differ.
# These are kept here as expect-fail markers so we notice if the
# behavior changes (e.g. runtime stops being lenient).
# ---------------------------------------------------------------------------


@pytest.mark.xfail(reason="runtime evaluates os.getenv; AST records None and warns")
def test_diff_function_call_default_diverges():
    source = """
import dagger
import os

@dagger.object_type
class Foo:
    @dagger.function
    def hello(self, name: str = os.environ.get("HOME", "x")) -> str:
        return name
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)
from pathlib import Path

from dagger.mod._analyzer.analyze import analyze_module, analyze_source_string
from ._runtime_introspect import runtime_introspect, runtime_introspect_package
def _both_pkg(files: dict[str, str], main: str, *, tmp_path: Path):
    """Multi-file variant of ``_both``.

    Materialises ``files`` under ``tmp_path``, runs ``analyze_module`` on
    every ``.py`` file, then runs ``runtime_introspect_package`` against
    the same tree imported as a real Python package.
    """
    pkg_dir = tmp_path / "ast_pkg"
    pkg_dir.mkdir()
    paths: list[Path] = []
    for rel, content in files.items():
        target = pkg_dir / rel
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content, encoding="utf-8")
        if rel.endswith(".py"):
            paths.append(target)
    if not any(rel == "__init__.py" for rel in files):
        init = pkg_dir / "__init__.py"
        init.write_text("", encoding="utf-8")
        paths.append(init)
    ast_md = analyze_module(source_files=paths, main_object_name=main)
    runtime_md = runtime_introspect_package(files, main)
    return ast_md, runtime_md




# ---------------------------------------------------------------------------
# Multi-file fixtures — exercise patterns that need a real package on disk:
# relative imports, cross-file aliases, cross-file constants.
# ---------------------------------------------------------------------------


def test_diff_multifile_cross_file_type_alias(tmp_path: Path):
    """Alias defined in ``types.py`` and imported via ``from .types``."""
    files = {
        "__init__.py": "",
        "types.py": (
            "import dagger\n"
            "from typing import Annotated\n"
            'Source = Annotated[dagger.Directory, dagger.DefaultPath(".")]\n'
        ),
        "main.py": (
            "import dagger\n"
            "from .types import Source\n"
            "\n"
            "@dagger.object_type\n"
            "class Foo:\n"
            "    @dagger.function\n"
            "    def build(self, src: Source) -> dagger.Container: ...\n"
        ),
    }
    ast_md, runtime_md = _both_pkg(files, "Foo", tmp_path=tmp_path)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_multifile_cross_file_constant_default(tmp_path: Path):
    """Constant defined in ``constants.py`` used as a default in main.py."""
    files = {
        "__init__.py": "",
        "constants.py": 'DEFAULT_NAME = "alice"\n',
        "main.py": (
            "import dagger\n"
            "from .constants import DEFAULT_NAME\n"
            "\n"
            "@dagger.object_type\n"
            "class Foo:\n"
            "    @dagger.function\n"
            "    def hello(self, name: str = DEFAULT_NAME) -> str:\n"
            "        return name\n"
        ),
    }
    ast_md, runtime_md = _both_pkg(files, "Foo", tmp_path=tmp_path)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_multifile_inherited_field_across_files(tmp_path: Path):
    """A dataclass-like base in one file, child in another."""
    files = {
        "__init__.py": "",
        "base.py": (
            "import dagger\n"
            "\n"
            "@dagger.object_type\n"
            "class Base:\n"
            '    name: str = dagger.field(default="from-base")\n'
        ),
        "main.py": (
            "import dagger\n"
            "from .base import Base\n"
            "\n"
            "@dagger.object_type\n"
            "class Foo(Base):\n"
            "    @dagger.function\n"
            "    def greet(self) -> str:\n"
            '        return "hello " + self.name\n'
        ),
    }
    ast_md, runtime_md = _both_pkg(files, "Foo", tmp_path=tmp_path)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_multifile_relative_import_decorated_class(tmp_path: Path):
    """Decorated class lives in helpers.py, used as a return type in main."""
    files = {
        "__init__.py": "",
        "helpers.py": (
            "import dagger\n"
            "\n"
            "@dagger.object_type\n"
            "class Helper:\n"
            '    name: str = dagger.field(default="h")\n'
        ),
        "main.py": (
            "import dagger\n"
            "from .helpers import Helper\n"
            "\n"
            "@dagger.object_type\n"
            "class Foo:\n"
            "    @dagger.function\n"
            "    def get_helper(self) -> Helper:\n"
            "        return Helper()\n"
        ),
    }
    ast_md, runtime_md = _both_pkg(files, "Foo", tmp_path=tmp_path)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_multifile_chained_cross_file_alias(tmp_path: Path):
    """``main`` imports B; ``types`` defines ``A = Directory; B = A``."""
    files = {
        "__init__.py": "",
        "types.py": (
            "import dagger\n"
            "A = dagger.Directory\n"
            "B = A\n"
        ),
        "main.py": (
            "import dagger\n"
            "from .types import B\n"
            "\n"
            "@dagger.object_type\n"
            "class Foo:\n"
            "    @dagger.function\n"
            "    def build(self, src: B) -> dagger.Container: ...\n"
        ),
    }
    ast_md, runtime_md = _both_pkg(files, "Foo", tmp_path=tmp_path)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_multifile_subpackage(tmp_path: Path):
    """Decorated class lives in a subpackage (``pkg/sub/types.py``)."""
    files = {
        "__init__.py": "",
        "sub/__init__.py": "",
        "sub/types.py": (
            "import dagger\n"
            "\n"
            "@dagger.object_type\n"
            "class Helper:\n"
            '    name: str = dagger.field(default="sub")\n'
        ),
        "main.py": (
            "import dagger\n"
            "from .sub.types import Helper\n"
            "\n"
            "@dagger.object_type\n"
            "class Foo:\n"
            "    @dagger.function\n"
            "    def get_helper(self) -> Helper:\n"
            "        return Helper()\n"
        ),
    }
    ast_md, runtime_md = _both_pkg(files, "Foo", tmp_path=tmp_path)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_multifile_pep695_type_alias_across_files(tmp_path: Path):
    """``type Source = …`` in types.py, imported via ``from .types``."""
    files = {
        "__init__.py": "",
        "types.py": (
            "import dagger\n"
            "from typing import Annotated\n"
            'type Source = Annotated[dagger.Directory, dagger.DefaultPath(".")]\n'
        ),
        "main.py": (
            "import dagger\n"
            "from .types import Source\n"
            "\n"
            "@dagger.object_type\n"
            "class Foo:\n"
            "    @dagger.function\n"
            "    def build(self, src: Source) -> dagger.Container: ...\n"
        ),
    }
    ast_md, runtime_md = _both_pkg(files, "Foo", tmp_path=tmp_path)
    assert_metadata_equivalent(ast_md, runtime_md)
