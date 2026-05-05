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

import pytest

from dagger.mod._analyzer.analyze import analyze_source_string

from ._differential import assert_metadata_equivalent
from ._runtime_introspect import runtime_introspect


def _both(source: str, main: str = "Foo", **kwargs):
    """Run both analyzers and return ``(ast_metadata, runtime_metadata)``."""
    ast_md = analyze_source_string(source, main)
    runtime_md = runtime_introspect(source, main)
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
