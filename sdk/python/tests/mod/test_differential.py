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

Python version policy: most fixtures use syntax that works on every
supported version (3.10+). Fixtures that depend on a feature added in
a later release carry a ``@requires_py3XX`` marker so the SDK's
existing CI matrix automatically exercises each pattern on every
version that can actually run it — and skips it on older ones rather
than failing with a misleading SyntaxError.
"""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

from dagger.mod._analyzer.analyze import analyze_module, analyze_source_string

from ._differential import assert_metadata_equivalent
from ._runtime_introspect import runtime_introspect, runtime_introspect_package

# Version markers for fixtures that use syntax/runtime features added in a
# specific Python release. The CI matrix runs the SDK suite on 3.10-3.14;
# these markers ensure each version-gated fixture only fires on a runtime
# that can actually parse and evaluate it.
requires_py311 = pytest.mark.skipif(
    sys.version_info < (3, 11),
    reason="typing.Self introduced in Python 3.11",
)
requires_py312 = pytest.mark.skipif(
    sys.version_info < (3, 12),
    reason="PEP 695 ``type X = …`` introduced in Python 3.12",
)


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


@requires_py312
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


@requires_py311
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


@requires_py311
def test_diff_inherited_function_only_from_undecorated_base():
    """Undecorated bases contribute @function methods only, not fields.

    Python MRO surfaces the inherited ``@function`` regardless; the
    dagger field path goes through ``dataclasses.fields`` which only
    walks dataclass parents. The differential test confirms both sides
    agree on this distinction.
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


@requires_py311
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


# ---------------------------------------------------------------------------
# Forward references — strings standing in for not-yet-defined types.
# ---------------------------------------------------------------------------


def test_diff_forward_ref_self_class():
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


def test_diff_forward_ref_in_optional():
    source = """
import dagger
from typing import Optional

@dagger.object_type
class Foo:
    @dagger.function
    def maybe_me(self, other: Optional["Foo"] = None) -> "Foo":
        return self
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_forward_ref_in_list():
    source = """
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    def cousins(self) -> list["Foo"]:
        return [self]
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_forward_ref_to_class_defined_later():
    source = """
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    def get_other(self) -> "Other":
        return Other()

@dagger.object_type
class Other:
    @dagger.function
    def name(self) -> str:
        return "other"
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_forward_ref_inside_annotated():
    source = """
import dagger
from typing import Annotated
from dagger import Doc

@dagger.object_type
class Foo:
    @dagger.function
    def echo(self, msg: Annotated["str", Doc("the message")]) -> str:
        return msg
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_future_annotations_makes_everything_strings():
    """``from __future__ import annotations`` makes every annotation a string.

    Both paths should agree on the resolved metadata as if the import
    weren't present.
    """
    source = """
from __future__ import annotations

import dagger
from typing import Optional, Annotated
from dagger import Doc, DefaultPath

@dagger.object_type
class Foo:
    name: str = dagger.field(default="x")

    @dagger.function
    def hello(
        self,
        msg: Annotated[str, Doc("a message")] = "world",
        src: Optional[Annotated[dagger.Directory, DefaultPath(".")]] = None,
        peer: Optional[Foo] = None,
    ) -> Foo:
        return self
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


# ---------------------------------------------------------------------------
# Annotated — qualified, aliased, multi-metadata, nested.
# ---------------------------------------------------------------------------


def test_diff_qualified_typing_annotated():
    source = """
import dagger
import typing
from dagger import Doc

@dagger.object_type
class Foo:
    @dagger.function
    def echo(self, msg: typing.Annotated[str, Doc("hi")]) -> str:
        return msg
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_typing_extensions_annotated():
    source = """
import dagger
from typing_extensions import Annotated, Doc

@dagger.object_type
class Foo:
    @dagger.function
    def echo(self, msg: Annotated[str, Doc("hi")]) -> str:
        return msg
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_aliased_annotated_import():
    source = """
import dagger
from typing import Annotated as A
from dagger import Doc

@dagger.object_type
class Foo:
    @dagger.function
    def echo(self, msg: A[str, Doc("hi")]) -> str:
        return msg
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_annotated_with_multiple_metadata():
    """``Annotated[T, Doc, Name, Deprecated]`` — every metadata kind at once."""
    source = """
import dagger
from typing import Annotated
from dagger import Doc, Name, Deprecated

@dagger.object_type
class Foo:
    @dagger.function
    def echo(
        self,
        msg: Annotated[
            str,
            Doc("the message"),
            Name("alt_msg"),
            Deprecated("use new_echo"),
        ] = "x",
    ) -> str:
        return msg
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_annotated_with_ignore_metadata():
    source = """
import dagger
from typing import Annotated
from dagger import Ignore

@dagger.object_type
class Foo:
    @dagger.function
    def build(
        self,
        src: Annotated[dagger.Directory, Ignore(["**/.git", "node_modules"])],
    ) -> dagger.Container:
        ...
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_nested_annotated_flattens():
    """``Annotated[Annotated[T, X], Y]`` flattens to ``Annotated[T, X, Y]``.

    typing flattens nested Annotated forms at runtime; the AST analyzer
    should reach the same metadata set.
    """
    source = """
import dagger
from typing import Annotated
from dagger import Doc, DefaultPath

# Both layers carry metadata. Runtime flattens to one Annotated[T, X, Y].
NestedSource = Annotated[
    Annotated[dagger.Directory, DefaultPath(".")],
    Doc("source directory"),
]

@dagger.object_type
class Foo:
    @dagger.function
    def build(self, src: NestedSource) -> dagger.Container:
        ...
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


# ---------------------------------------------------------------------------
# Optional / Union spelling variants — the runtime treats them as equivalent.
# ---------------------------------------------------------------------------


def test_diff_typing_optional_qualified():
    source = """
import dagger
import typing

@dagger.object_type
class Foo:
    @dagger.function
    def hello(self, name: typing.Optional[str] = None) -> str:
        return name or "x"
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_typing_union_with_none():
    source = """
import dagger
from typing import Union

@dagger.object_type
class Foo:
    @dagger.function
    def hello(self, name: Union[str, None] = None) -> str:
        return name or "x"
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_pep604_none_first():
    """``None | X`` is the same as ``X | None`` at runtime."""
    source = """
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    def hello(self, name: None | str = None) -> str:
        return name or "x"
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_aliased_optional_import():
    source = """
import dagger
from typing import Optional as Opt

@dagger.object_type
class Foo:
    @dagger.function
    def hello(self, name: Opt[str] = None) -> str:
        return name or "x"
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


# ---------------------------------------------------------------------------
# Container spellings — list / List / Sequence / collections.abc.Sequence.
# ---------------------------------------------------------------------------


def test_diff_typing_list_uppercase():
    """``from typing import List`` then ``List[T]`` (deprecated alias)."""
    source = """
import dagger
from typing import List

@dagger.object_type
class Foo:
    @dagger.function
    def echo_list(self, items: List[str]) -> int:
        return len(items)
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_typing_sequence_qualified():
    source = """
import dagger
import typing

@dagger.object_type
class Foo:
    @dagger.function
    def echo_list(self, items: typing.Sequence[str]) -> int:
        return len(items)
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_collections_abc_sequence():
    source = """
import dagger
from collections.abc import Sequence

@dagger.object_type
class Foo:
    @dagger.function
    def echo_list(self, items: Sequence[str]) -> int:
        return len(items)
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_tuple_with_ellipsis():
    """``tuple[T, ...]`` (homogeneous tuple) maps to a list of T."""
    source = """
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    def echo_tuple(self, items: tuple[str, ...]) -> int:
        return len(items)
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


# ---------------------------------------------------------------------------
# Self qualified / nested.
# ---------------------------------------------------------------------------


@requires_py311
def test_diff_self_qualified_typing():
    source = """
import dagger
import typing

@dagger.object_type
class Foo:
    @dagger.function
    def me(self) -> typing.Self:
        return self
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


@requires_py311
def test_diff_self_in_list():
    source = """
import dagger
from typing import Self

@dagger.object_type
class Foo:
    @dagger.function
    def cousins(self) -> list[Self]:
        return [self]
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


@requires_py311
def test_diff_self_optional():
    source = """
import dagger
from typing import Self, Optional

@dagger.object_type
class Foo:
    @dagger.function
    def maybe(self) -> Optional[Self]:
        return self
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


# ---------------------------------------------------------------------------
# Decorator and field() argument shapes.
# ---------------------------------------------------------------------------


def test_diff_function_decorator_call_no_args():
    source = """
import dagger

@dagger.object_type
class Foo:
    @dagger.function()
    def hello(self) -> str:
        return "hi"
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_function_decorator_full_kwargs():
    source = """
import dagger

@dagger.object_type
class Foo:
    @dagger.function(name="echoMessage", deprecated="use echo2 instead")
    def echo(self, msg: str) -> str:
        return msg

    @dagger.function(name="echo2")
    def echo2(self, msg: str) -> str:
        return msg
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_field_with_callable_default_list():
    """``dagger.field(default=list)`` uses a callable default for an empty list.

    Unlike ``dataclasses.field`` (``default_factory=list``), dagger's
    ``field`` overloads ``default`` to accept either a value or a
    0-argument callable.
    """
    source = """
import dagger

@dagger.object_type
class Foo:
    items: list[str] = dagger.field(default=list)

    @dagger.function
    def count(self) -> int:
        return len(self.items)
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_field_with_init_false():
    source = """
import dagger

@dagger.object_type
class Foo:
    name: str = dagger.field(default="x")
    computed: str = dagger.field(default="cached", init=False)

    @dagger.function
    def hello(self) -> str:
        return self.name + self.computed
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_field_with_alt_name():
    source = """
import dagger

@dagger.object_type
class Foo:
    from_: str = dagger.field(name="from", default="src")

    @dagger.function
    def hello(self) -> str:
        return self.from_
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


# ---------------------------------------------------------------------------
# Misc — InitVar, default values of various shapes.
# ---------------------------------------------------------------------------


def test_diff_initvar():
    source = """
import dagger
from dataclasses import InitVar

@dagger.object_type
class Foo:
    name: str = dagger.field(default="x")
    seed: InitVar[int] = 42

    def __post_init__(self, seed: int) -> None:
        self.name = self.name + str(seed)

    @dagger.function
    def hello(self) -> str:
        return self.name
"""
    ast_md, runtime_md = _both(source)
    # Both paths should expose ``seed`` as a constructor parameter but not
    # as a field. The differential comparator only checks fields/functions
    # by default; constructor diffs are out of scope here because the AST
    # synthesises them while the runtime relies on dataclass machinery.
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_negative_int_default():
    source = """
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    def hello(self, offset: int = -5) -> int:
        return offset
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


def test_diff_bytes_param_type():
    source = """
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    def echo(self, payload: bytes) -> bytes:
        return payload
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


# ---------------------------------------------------------------------------
# Dagger scalar types (Platform, JSON) reach the schema as ``scalar``.
# ---------------------------------------------------------------------------


def test_diff_dagger_scalar_types():
    source = """
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    def render(self, payload: dagger.JSON) -> dagger.Platform:
        ...
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)


# ---------------------------------------------------------------------------
# Kitchen sink — one big realistic-looking module touching many patterns.
# Reads as documentation: "all of this is supported and matches runtime."
# ---------------------------------------------------------------------------


KITCHEN_SINK = """
from __future__ import annotations

import dagger
import typing
from dataclasses import InitVar
from enum import IntEnum
from typing import Annotated, Optional, Self
from typing_extensions import Doc

from dagger import DefaultPath, Deprecated, Ignore, Name


# Module-level type alias (also tested as PEP 695 in another fixture).
Source = Annotated[dagger.Directory, DefaultPath(".")]


@dagger.enum_type
class Priority(IntEnum):
    LOW = 1
    HIGH = 100


@dagger.object_type
class Base:
    name: str = dagger.field(default="anon")

    @dagger.function
    def with_name(self, n: str) -> Self:
        self.name = n
        return self


@dagger.object_type
class Foo(Base):
    # Field-shape variants.
    items: list[str] = dagger.field(default=list)
    from_: str = dagger.field(name="from", default="here")
    legacy: str = dagger.field(default="x", deprecated="prefer 'name'")
    cached: str = dagger.field(default="c", init=False)

    # InitVar contributes a constructor parameter, not a field.
    seed: InitVar[int] = 0

    def __post_init__(self, seed: int) -> None:
        self.cached = "cached-" + str(seed)

    # Aliased / qualified annotation forms — both paths should agree.
    @dagger.function
    def echo(self, msg: typing.Annotated[str, Doc("the message")] = "x") -> str:
        return msg

    @dagger.function
    async def echo_async(self, msg: Annotated[str, Doc("async msg")]) -> str:
        return msg

    # Self in different shapes.
    @dagger.function
    def me(self) -> Self:
        return self

    @dagger.function
    def cousins(self) -> list[Self]:
        return [self]

    @dagger.function
    def maybe(self) -> Optional[Self]:
        return self

    # Optional[Annotated[T, X]] — metadata recursion.
    @dagger.function
    def build(
        self,
        src: Optional[Annotated[dagger.Directory, DefaultPath(".")]] = None,
        ignore: Annotated[dagger.Directory, Ignore(["**/.git"])] | None = None,
    ) -> dagger.Container:
        ...

    # Multi-metadata Annotated.
    @dagger.function
    def deprecated_echo(
        self,
        msg: Annotated[
            str,
            Doc("legacy"),
            Name("legacy_msg"),
            Deprecated("use echo"),
        ] = "x",
    ) -> str:
        return msg

    # Type-alias parameter.
    @dagger.function
    def from_alias(self, src: Source) -> dagger.Container:
        ...

    # Forward ref to a class defined later.
    @dagger.function
    def get_other(self) -> "Other":
        return Other()

    # Static method with explicit @function.
    @staticmethod
    @dagger.function
    def shout(msg: str) -> str:
        return msg.upper()

    # Decorators with kwargs.
    @dagger.function(name="renamed", deprecated="use rename")
    def rename(self, n: str) -> Self:
        self.name = n
        return self

    # Enum return type.
    @dagger.function
    def priority(self) -> Priority:
        return Priority.HIGH

    # Container variants.
    @dagger.function
    def echo_list(self, items: list[str]) -> int:
        return len(items)

    @dagger.function
    def echo_tuple(self, items: tuple[str, ...]) -> int:
        return len(items)

    # Negative default and bytes type.
    @dagger.function
    def offset(self, n: int = -1) -> int:
        return n

    @dagger.function
    def raw(self, payload: bytes) -> bytes:
        return payload


@dagger.object_type
class Other:
    @dagger.function
    def name(self) -> str:
        return "other"
"""


@requires_py311
def test_diff_kitchen_sink():
    """One realistic module exercising many supported patterns at once.

    If a single addition to the analyzer breaks one of these patterns,
    this test localises *which* member diverged thanks to
    ``assert_metadata_equivalent``'s path-prefixed diffs. Reading the
    fixture top-to-bottom is also the closest thing to a
    "what does the analyzer cover?" cheat sheet.
    """
    ast_md, runtime_md = _both(KITCHEN_SINK)
    assert_metadata_equivalent(ast_md, runtime_md)


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
        "types.py": ("import dagger\nA = dagger.Directory\nB = A\n"),
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


@requires_py312
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


# ---------------------------------------------------------------------------
# Real-world pattern caught by the one-off corpus run: module-level
# constants used as arguments inside Annotated metadata.
# ---------------------------------------------------------------------------


def test_diff_ignore_with_module_level_constant():
    """``Ignore(SOURCE_IGNORE)`` and ``Doc(MESSAGE)`` resolve through constants.

    Pattern observed in misty-step/thinktank and interTwin-eu/itwinai/ci —
    users factor a long ignore list (and a doc string) out as a
    module-level constant for readability. Pre-fix, the AST analyzer
    silently dropped both because the metadata extractor only honoured
    literal arguments. Now both paths agree.
    """
    source = """
import dagger
from typing import Annotated
from dagger import Doc, Ignore

SOURCE_IGNORE = [
    ".git",
    "node_modules",
    "_build",
    ".cache",
]
SOURCE_DOC = "Repo source directory"

@dagger.object_type
class Foo:
    @dagger.function
    async def build(
        self,
        source: Annotated[
            dagger.Directory,
            Ignore(SOURCE_IGNORE),
            Doc(SOURCE_DOC),
        ],
    ) -> dagger.Container:
        ...
"""
    ast_md, runtime_md = _both(source)
    assert_metadata_equivalent(ast_md, runtime_md)
