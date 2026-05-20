"""Visitor for extracting Annotated metadata from type annotations."""

from __future__ import annotations

import ast
import dataclasses
from collections.abc import Callable
from typing import Any

# A callable that resolves a name reference (or other AST expression) to its
# Python value. Used to follow ``Ignore(SOURCE_IGNORE)`` and
# ``Doc(SOME_CONST)`` through the parser's module-level constant map so the
# AST analyzer reaches the same value the runtime does. ``None`` skips
# resolution and only literal arguments are honoured.
ConstantResolver = Callable[[ast.expr], Any]


@dataclasses.dataclass
class AnnotatedMetadata:
    """Metadata extracted from Annotated[T, ...] type."""

    base_type: ast.expr  # The base type T
    doc: str | None = None  # From Doc()
    name: str | None = None  # From Name()
    default_path: str | None = None  # From DefaultPath()
    default_address: str | None = None  # From DefaultAddress()
    ignore: list[str] | None = None  # From Ignore()
    deprecated: str | None = None  # From Deprecated()


def is_annotated_type(annotation: ast.expr) -> bool:
    """Check if an annotation is Annotated[T, ...].

    Handles:
    - Annotated[T, ...]
    - typing.Annotated[T, ...]
    - typing_extensions.Annotated[T, ...]
    """
    if not isinstance(annotation, ast.Subscript):
        return False

    value = annotation.value
    if isinstance(value, ast.Name):
        return value.id == "Annotated"
    if isinstance(value, ast.Attribute):
        return value.attr == "Annotated"

    return False


def find_annotated(  # noqa: C901, PLR0912 — annotation-shape dispatch
    annotation: ast.expr,
) -> ast.expr | None:
    """Find an ``Annotated[...]`` node inside Optional/Union/``| None`` wrappings.

    Returns the ``Annotated`` subscript when one is reachable through any
    sequence of nullable wrappers (``Optional[Annotated[T, …]]``,
    ``Annotated[T, …] | None``, ``Union[Annotated[T, …], None]``), or
    ``None`` when no such inner Annotated exists. ``list[Annotated[T, …]]``
    and other generic containers are *not* traversed — element-level
    metadata doesn't apply to the parameter as a whole.
    """
    if is_annotated_type(annotation):
        return annotation

    # ``Optional[X]`` / ``Union[X, None]``
    if isinstance(annotation, ast.Subscript):
        base_name = None
        if isinstance(annotation.value, ast.Name):
            base_name = annotation.value.id
        elif isinstance(annotation.value, ast.Attribute):
            base_name = annotation.value.attr
        if base_name == "Optional":
            return find_annotated(annotation.slice)
        if base_name == "Union":
            slice_val = annotation.slice
            args = slice_val.elts if isinstance(slice_val, ast.Tuple) else [slice_val]
            for arg in args:
                inner = find_annotated(arg)
                if inner is not None:
                    return inner
        return None

    # ``X | None`` / ``None | X``
    if isinstance(annotation, ast.BinOp) and isinstance(annotation.op, ast.BitOr):
        for side in (annotation.left, annotation.right):
            if isinstance(side, ast.Constant) and side.value is None:
                continue
            if isinstance(side, ast.Name) and side.id in ("None", "NoneType"):
                continue
            inner = find_annotated(side)
            if inner is not None:
                return inner

    return None


def extract_annotated_metadata(
    annotation: ast.expr,
    resolver: ConstantResolver | None = None,
) -> AnnotatedMetadata:
    """Extract metadata from an Annotated[T, ...] type annotation.

    Drills through nullable wrappers (``Optional[Annotated[…]]``,
    ``Annotated[…] | None``, ``Union[Annotated[…], None]``) so the
    metadata still applies — these wrappings are about cardinality, not
    a different type. Generic containers (``list[Annotated[…]]``) are
    not traversed; element-level metadata doesn't apply to the
    parameter as a whole.

    Args:
        annotation: An AST node representing an Annotated type.
        resolver: Optional callable that resolves a name reference to its
            value. When provided, ``Ignore(SOURCE_IGNORE)``,
            ``Doc(MESSAGE)``, etc. follow the name through the parser's
            module-level constant map so the result matches what
            ``typing.get_type_hints`` would compute at runtime.

    Returns
    -------
        AnnotatedMetadata with the base type and extracted metadata.
    """
    annotated = find_annotated(annotation)
    if annotated is None:
        return AnnotatedMetadata(base_type=annotation)
    annotation = annotated

    # Get the subscript slice (the contents of Annotated[...])
    subscript = annotation.slice  # type: ignore[attr-defined]

    # Handle different Python versions / AST structures
    if isinstance(subscript, ast.Tuple):
        elements = subscript.elts
    elif isinstance(subscript, ast.Index):
        # Python 3.8 compatibility
        if isinstance(subscript.value, ast.Tuple):  # type: ignore[attr-defined]
            elements = subscript.value.elts  # type: ignore[attr-defined]
        else:
            elements = [subscript.value]  # type: ignore[attr-defined]
    else:
        # Single element (shouldn't happen for valid Annotated)
        return AnnotatedMetadata(base_type=subscript)

    if not elements:
        return AnnotatedMetadata(base_type=annotation)

    # First element is the base type. If it is itself an Annotated, the
    # runtime ``typing.Annotated`` flattens metadata (``Annotated[Annotated[
    # T, X], Y]`` is equivalent to ``Annotated[T, X, Y]``). Mirror that by
    # recursing and merging the inner metadata so users don't have to
    # remember the flat-vs-nested distinction.
    base_type = elements[0]
    if is_annotated_type(base_type):
        metadata = extract_annotated_metadata(base_type, resolver=resolver)
    else:
        metadata = AnnotatedMetadata(base_type=base_type)

    # Outer metadata takes precedence over inner only when the outer
    # explicitly sets a value — preserve any inner attribute the outer
    # didn't touch.
    for meta_node in elements[1:]:
        _extract_single_metadata(meta_node, metadata, resolver=resolver)

    return metadata


def _extract_single_metadata(
    node: ast.expr,
    metadata: AnnotatedMetadata,
    *,
    resolver: ConstantResolver | None = None,
) -> None:
    """Extract a single metadata item and update the metadata object.

    Handles:
    - Doc("description")
    - Name("api_name")
    - DefaultPath("path")
    - DefaultAddress("address")
    - Ignore(["pattern1", "pattern2"])
    - Deprecated("reason")
    """
    if not isinstance(node, ast.Call):
        return

    # Get the function name being called
    func_name = _get_call_name(node)
    if not func_name:
        return

    # Map known metadata types to extraction
    if func_name in ("Doc", "typing_extensions.Doc"):
        metadata.doc = _extract_string_arg(node, resolver=resolver)
    elif func_name in ("Name", "dagger.Name"):
        metadata.name = _extract_string_arg(node, resolver=resolver)
    elif func_name in ("DefaultPath", "dagger.DefaultPath"):
        metadata.default_path = _extract_string_arg(node, resolver=resolver)
    elif func_name in ("DefaultAddress", "dagger.DefaultAddress"):
        metadata.default_address = _extract_string_arg(node, resolver=resolver)
    elif func_name in ("Ignore", "dagger.Ignore"):
        metadata.ignore = _extract_list_arg(node, resolver=resolver)
    elif func_name in ("Deprecated", "dagger.Deprecated"):
        metadata.deprecated = _extract_string_arg(node, default="", resolver=resolver)


def _get_call_name(node: ast.Call) -> str | None:
    """Get the name of a Call node's function.

    Handles:
    - Doc(...) -> "Doc"
    - typing_extensions.Doc(...) -> "typing_extensions.Doc"
    """
    func = node.func
    if isinstance(func, ast.Name):
        return func.id
    if isinstance(func, ast.Attribute):
        if isinstance(func.value, ast.Name):
            return f"{func.value.id}.{func.attr}"
        return func.attr
    return None


def _extract_string_arg(
    node: ast.Call,
    default: str | None = None,
    *,
    resolver: ConstantResolver | None = None,
) -> str | None:
    """Extract a string argument from a Call node.

    Looks for:
    - First positional argument: Doc("value")
    - Keyword argument with specific names: Doc(documentation="value")
    - A name reference resolved through ``resolver`` to a string value
      (so ``Doc(MESSAGE)`` works when ``MESSAGE = "…"`` lives at module
      scope).
    """
    # Check positional arguments
    if node.args:
        first_arg = node.args[0]
        if isinstance(first_arg, ast.Constant) and isinstance(first_arg.value, str):
            return first_arg.value
        if resolver is not None:
            value = resolver(first_arg)
            if isinstance(value, str):
                return value

    # Check keyword arguments
    for keyword in node.keywords:
        if isinstance(keyword.value, ast.Constant) and isinstance(
            keyword.value.value, str
        ):
            return keyword.value.value
        if resolver is not None:
            value = resolver(keyword.value)
            if isinstance(value, str):
                return value

    return default


def _extract_list_arg(
    node: ast.Call,
    *,
    resolver: ConstantResolver | None = None,
) -> list[str] | None:
    """Extract a list of strings argument from a Call node.

    Looks for:
    - First positional argument: Ignore(["a", "b"])
    - Keyword argument: Ignore(patterns=["a", "b"])
    - A name reference resolved through ``resolver`` to a list of strings
      (so ``Ignore(SOURCE_IGNORE)`` works when ``SOURCE_IGNORE = [...]``
      lives at module scope).
    """
    target_node: ast.expr | None = None

    # Check positional arguments
    if node.args:
        target_node = node.args[0]
    else:
        # Check keyword arguments
        for keyword in node.keywords:
            target_node = keyword.value
            break

    if target_node is None:
        return None

    # Literal list — the original supported shape.
    if isinstance(target_node, ast.List):
        result = [
            el.value
            for el in target_node.elts
            if isinstance(el, ast.Constant) and isinstance(el.value, str)
        ]
        return result or None

    # Name reference — resolve through the parser's constant map.
    if resolver is not None:
        value = resolver(target_node)
        if isinstance(value, (list, tuple)):
            result = [v for v in value if isinstance(v, str)]
            return result or None

    return None


def unwrap_annotated(annotation: ast.expr) -> ast.expr:
    """Remove Annotated wrapper and return the base type.

    If the annotation is not Annotated, returns it unchanged.
    """
    if not is_annotated_type(annotation):
        return annotation

    subscript = annotation.slice  # type: ignore[attr-defined]

    if isinstance(subscript, ast.Tuple):
        return subscript.elts[0]
    if isinstance(subscript, ast.Index):
        inner = subscript.value  # type: ignore[attr-defined]
        if isinstance(inner, ast.Tuple):
            return inner.elts[0]
        return inner

    return annotation


def get_annotation_string(annotation: ast.expr) -> str:
    """Get a string representation of an annotation."""
    return ast.unparse(annotation)
