import ast
import builtins
import contextlib
import dataclasses
import enum
import functools
import importlib
import importlib.util
import inspect
import operator
import typing
from collections.abc import Callable, Coroutine
from typing import Any, TypeAlias, TypeVar, cast

import anyio
import anyio.from_thread
import anyio.to_thread
import typing_extensions
from beartype.door import TypeHint, UnionTypeHint, is_subhint
from cattrs.cols import is_sequence
from graphql.pyutils import snake_to_camel

from dagger.client.base import Type
from dagger.mod._arguments import DefaultPath, Deprecated, Ignore, Name
from dagger.mod._types import ContextPath

asyncify = anyio.to_thread.run_sync
syncify = anyio.from_thread.run

T = TypeVar("T")

AwaitableOrValue: TypeAlias = Coroutine[Any, Any, T] | T

if typing.TYPE_CHECKING:
    from dagger.mod._module import Module
    from dagger.mod._resolver import ObjectType


@dataclasses.dataclass(slots=True)
class EnumMemberDoc:
    description: str | None = None
    deprecated: str | None = None


async def await_maybe(value: AwaitableOrValue[T]) -> T:
    return await value if inspect.iscoroutine(value) else cast(T, value)


def to_pascal_case(s: str) -> str:
    """Convert a string to PascalCase."""
    return snake_to_camel(s.replace("-", "_"))


def to_camel_case(s: str) -> str:
    """Convert a string to camelCase."""
    return snake_to_camel(s.replace("-", "_"), upper=False)


def normalize_name(name: str) -> str:
    """Remove the last underscore, used to avoid conflicts with reserved words."""
    if name.endswith("_") and name[-2] != "_" and not name.startswith("_"):
        return name.removesuffix("_")
    return name


def get_meta(obj: Any, match: type[T]) -> T | None:
    """Get metadata from an annotated type."""
    if is_initvar(obj):
        return get_meta(obj.type, match)
    if not is_annotated(obj):
        return None
    return next(
        (arg for arg in reversed(typing.get_args(obj)) if isinstance(arg, match)),
        None,
    )


def get_doc(obj: Any) -> str | None:
    """Get the last Doc() in an annotated type or the docstring of an object."""
    if annotated := get_meta(obj, typing_extensions.Doc):
        return annotated.documentation

    # Avoid getting docs from builtins.
    # We're only interested in things we decorate.
    if inspect.getmodule(obj) == builtins or (
        not inspect.isclass(obj) and not inspect.isroutine(obj)
    ):
        return None

    # Don't look in base classes (otherwise just use inspect.get_doc).
    try:
        doc = obj.__doc__
    except AttributeError:
        return None
    if not isinstance(doc, str):
        return None

    # By default, a dataclass's __doc__ will be the signature of the class,
    # not None.
    if (
        doc
        and dataclasses.is_dataclass(obj)
        and doc.startswith(f"{obj.__name__}(")
        and doc.endswith(")")
    ):
        return None

    return inspect.cleandoc(doc)


def get_ignore(obj: Any) -> list[str] | None:
    """Get the last Ignore() of an annotated type."""
    meta = get_meta(obj, Ignore)
    return meta.patterns if meta else None


def get_default_path(obj: Any) -> ContextPath | None:
    """Get the last DefaultPath() of an annotated type."""
    meta = get_meta(obj, DefaultPath)
    return meta.from_context if meta else None


def get_alt_name(annotation: type) -> str | None:
    """Get an alternative name in last Name() of an annotated type."""
    return annotated.name if (annotated := get_meta(annotation, Name)) else None


def get_deprecated(obj: Any) -> str | None:
    """Get the deprecation metadata from an annotated type."""
    if meta := get_meta(obj, Deprecated):
        return meta.reason
    return None


def is_union(th: TypeHint) -> bool:
    """Check if the unsubscripted part of a type is a Union."""
    return isinstance(th, UnionTypeHint)


def is_nullable(th: TypeHint) -> bool:
    """Check if the annotation is SomeType | None.

    Does not support Annotated types. Use only on types that have been
    resolved with get_type_hints.
    """
    return th.is_bearable(None)


def non_null(th: TypeHint) -> TypeHint:
    """Remove None from a union.

    Does not support Annotated types. Use only on types that have been
    resolved with get_type_hints.
    """
    if TypeHint(None) not in th:
        return th

    args = (x for x in th.args if x is not type(None))
    return TypeHint(functools.reduce(operator.or_, args))


_T = TypeVar("_T", bound=type)
Obj_T = TypeVar("Obj_T", bound=Type)


def is_self(annotation: type) -> typing.TypeGuard[type]:
    """Check if an annotatino is a Self type."""
    # Typing extensions should return typing.Self if it exists (Python 3.11+)
    return annotation is typing_extensions.Self


def is_annotated(annotation: type) -> bool:
    """Check if the given type is an annotated type."""
    return typing.get_origin(annotation) in (
        typing.Annotated,
        typing_extensions.Annotated,
    )


def strip_annotations(t: _T) -> _T:
    """Strip the annotations from a given type."""
    return strip_annotations(typing.get_args(t)[0]) if is_annotated(t) else t


def is_list_type(t: Any) -> typing.TypeGuard[typing.Sequence]:
    """Check if an annotation represents a list."""
    return is_sequence(t)


def list_of(t: typing.Any) -> type | None:
    """Retrieve a list's element type or None if not a list."""
    if not is_list_type(t):
        return None
    th = TypeHint(t)
    try:
        return th.args[0]
    except IndexError:
        msg = (
            "Expected sequence type to be subscripted "
            f"with 1 subtype, got {len(th)}: {th.hint!r}"
        )
        raise TypeError(msg) from None


def is_list_of(v: Any, t: _T) -> typing.TypeGuard[typing.Sequence[_T]]:
    """Check if the annotation is a list of the given type."""
    return is_subhint(v, typing.Sequence[t])


def is_object_list_type(t: Any):
    """Check if the annotation is a list of an object client binding."""
    return is_list_of(t, Type)


def object_list_of(t: Any) -> type[Type] | None:
    """Retrive a list's element type or None if not a list of objects."""
    if is_object_list_type(t) and (el := list_of(t)):
        return cast(type[Type], el)
    return None


def is_dagger_object_type(t: typing.Any) -> typing.TypeGuard[type[Type]]:
    """Check if the annotation is an object client binding."""
    return is_subclass(t, Type)


def is_dagger_interface_type(t: typing.Any) -> typing.TypeGuard[type]:
    """Check if the annotation is an interface definition."""
    obj = get_object_type(t)
    return obj is not None and obj.interface and is_protocol(t)


def is_subclass(obj: type, bases) -> typing.TypeGuard[type]:
    """A safe version of issubclass (won't raise)."""
    try:
        return issubclass(obj, bases)
    except TypeError:
        return False


def is_protocol(t: Any) -> typing.TypeGuard[type]:
    """Check if the given type is a Protocol subclass."""
    return is_subclass(t, typing.Protocol) and getattr(t, "_is_protocol", False)


def is_initvar(annotation: type) -> typing.TypeGuard[dataclasses.InitVar]:
    """Check if the given type is a dataclasses.InitVar."""
    return annotation is dataclasses.InitVar or type(annotation) is dataclasses.InitVar


def is_mod_object_type(cls) -> bool:
    """Check if the given class was decorated with @object_type."""
    return hasattr(cls, "__dagger_object_type__")


def get_object_type(cls) -> "ObjectType | None":
    """Return the decorated object_type metadata on a class."""
    return getattr(cls, "__dagger_object_type__", None)


def get_module(cls) -> "Module | None":
    """Return the Module instance on a decorated object_type class."""
    return getattr(cls, "__dagger_module__", None)


def get_alt_constructor(cls: type[T]) -> Callable[..., T] | None:
    """Get classmethod named `create` from object type."""
    if inspect.isclass(cls) and is_mod_object_type(cls):
        fn = getattr(cls, "create", None)
        if inspect.ismethod(fn) and fn.__self__ is cls:
            return fn
    return None


def get_parent_module_doc(obj: type) -> str | None:
    """Get the docstring of the parent module."""
    spec = importlib.util.find_spec(obj.__module__)
    if not spec or not spec.parent:
        return None
    mod = importlib.import_module(spec.parent)
    return inspect.getdoc(mod)


def _extract_doc_from_next_stmt(class_body: list[ast.stmt], index: int) -> str | None:
    """Extract docstring from the statement following the given index."""
    next_idx = index + 1
    if next_idx >= len(class_body):
        return None

    next_stmt = class_body[next_idx]
    if (
        isinstance(next_stmt, ast.Expr)
        and isinstance(next_stmt.value, ast.Constant)
        and isinstance(next_stmt.value.value, str)
    ):
        return next_stmt.value.value.strip()
    return None


def _parse_enum_docstring(text: str) -> EnumMemberDoc:
    description_lines: list[str] = []
    deprecated_lines: list[str] = []
    lines = text.splitlines()
    it = iter(enumerate(lines))
    for _, raw_line in it:
        stripped = raw_line.strip()
        if stripped.startswith(".. deprecated::"):
            # capture first line after the directive
            remainder = stripped[len(".. deprecated::") :].strip()
            if remainder:
                deprecated_lines.append(remainder)
            # grab any indented continuation lines
            for _, cont in it:
                cont_stripped = cont.strip()
                if not cont_stripped:
                    continue
                if cont.startswith(("   ", "\t")):
                    deprecated_lines.append(cont_stripped)
                    continue
                # hit a non-indented line: feed it back into the outer loop
                description_lines.append(cont_stripped)
                break
        else:
            description_lines.append(stripped)
    description = "\n".join(line for line in description_lines if line).strip()
    deprecated = "\n".join(line for line in deprecated_lines if line).strip()
    return EnumMemberDoc(
        description=description or None,
        deprecated=deprecated or None,
    )


def extract_enum_member_doc(cls: type[enum.Enum]) -> dict[str, EnumMemberDoc]:
    """Extract docstrings for enum members by parsing the AST."""
    member_docs: dict[str, EnumMemberDoc] = {}

    with contextlib.suppress(OSError, TypeError, SyntaxError):
        source = inspect.getsource(cls)
        tree = ast.parse(source)

        # Find the class definition
        class_node = None
        for node in ast.walk(tree):
            if isinstance(node, ast.ClassDef) and node.name == cls.__name__:
                class_node = node
                break

        if class_node is not None:
            # Look for assignments followed by string literals
            for i, stmt in enumerate(class_node.body):
                if not isinstance(stmt, ast.Assign):
                    continue

                # Check if this is an enum member assignment
                for target in stmt.targets:
                    if isinstance(target, ast.Name):
                        member_name = target.id
                        doc = _extract_doc_from_next_stmt(class_node.body, i)
                        if doc:
                            member_docs[member_name] = _parse_enum_docstring(doc)

    return member_docs
