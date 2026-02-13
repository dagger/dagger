"""Visitor for extracting Annotated metadata from type annotations."""

from __future__ import annotations

import ast
import dataclasses


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


def extract_annotated_metadata(annotation: ast.expr) -> AnnotatedMetadata:
    """Extract metadata from an Annotated[T, ...] type annotation.

    Args:
        annotation: An AST node representing an Annotated type.

    Returns
    -------
        AnnotatedMetadata with the base type and extracted metadata.
    """
    if not is_annotated_type(annotation):
        return AnnotatedMetadata(base_type=annotation)

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

    # First element is the base type
    base_type = elements[0]

    # Remaining elements are metadata
    metadata = AnnotatedMetadata(base_type=base_type)

    for meta_node in elements[1:]:
        _extract_single_metadata(meta_node, metadata)

    return metadata


def _extract_single_metadata(node: ast.expr, metadata: AnnotatedMetadata) -> None:
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
        metadata.doc = _extract_string_arg(node)
    elif func_name in ("Name", "dagger.Name"):
        metadata.name = _extract_string_arg(node)
    elif func_name in ("DefaultPath", "dagger.DefaultPath"):
        metadata.default_path = _extract_string_arg(node)
    elif func_name in ("DefaultAddress", "dagger.DefaultAddress"):
        metadata.default_address = _extract_string_arg(node)
    elif func_name in ("Ignore", "dagger.Ignore"):
        metadata.ignore = _extract_list_arg(node)
    elif func_name in ("Deprecated", "dagger.Deprecated"):
        metadata.deprecated = _extract_string_arg(node, default="")


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


def _extract_string_arg(node: ast.Call, default: str | None = None) -> str | None:
    """Extract a string argument from a Call node.

    Looks for:
    - First positional argument: Doc("value")
    - Keyword argument with specific names: Doc(documentation="value")
    """
    # Check positional arguments
    if node.args:
        first_arg = node.args[0]
        if isinstance(first_arg, ast.Constant) and isinstance(first_arg.value, str):
            return first_arg.value

    # Check keyword arguments
    for keyword in node.keywords:
        if isinstance(keyword.value, ast.Constant) and isinstance(
            keyword.value.value, str
        ):
            return keyword.value.value

    return default


def _extract_list_arg(node: ast.Call) -> list[str] | None:
    """Extract a list of strings argument from a Call node.

    Looks for:
    - First positional argument: Ignore(["a", "b"])
    - Keyword argument: Ignore(patterns=["a", "b"])
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

    # Extract list elements
    if isinstance(target_node, ast.List):
        result = [
            el.value
            for el in target_node.elts
            if isinstance(el, ast.Constant) and isinstance(el.value, str)
        ]
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
