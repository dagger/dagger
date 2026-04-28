"""Type resolver for converting AST annotations to ResolvedType.

This module handles:
- Forward references ("Foo" -> Foo)
- Self type resolution
- Union types (T | None)
- Generic types (list[T])
- Dagger types (Container, Directory, etc.)
"""

from __future__ import annotations

import ast
import collections.abc
import contextlib
import enum
import logging
import types
import typing
from collections.abc import Iterator
from typing import Any, get_args, get_origin

import typing_extensions

from dagger.mod._analyzer.errors import TypeResolutionError
from dagger.mod._analyzer.metadata import LocationMetadata, ResolvedType
from dagger.mod._analyzer.namespace import StubNamespace, is_stub_type
from dagger.mod._analyzer.visitors.annotations import unwrap_annotated

logger = logging.getLogger(__name__)

# Known Dagger types from the API
DAGGER_OBJECT_TYPES = {
    "Container",
    "Directory",
    "File",
    "Secret",
    "Service",
    "CacheVolume",
    "Socket",
    "ModuleSource",
    "Module",
    "GitRepository",
    "GitRef",
    "Terminal",
    "Host",
    "Client",
}

# Dagger scalar types
DAGGER_SCALAR_TYPES = {
    "Platform",
    "JSON",
}

# Dagger enum types from the API
DAGGER_ENUM_TYPES = {
    "CacheSharingMode",
    "ChangesetMergeConflict",
    "ChangesetsMergeConflict",
    "ExistsType",
    "FileType",
    "FunctionCachePolicy",
    "ImageLayerCompression",
    "ImageMediaTypes",
    "ModuleSourceExperimentalFeature",
    "ModuleSourceKind",
    "NetworkProtocol",
    "ReturnType",
    "TypeDefKind",
}

# Primitive type mapping
PRIMITIVE_TYPES = {
    "str": "str",
    "int": "int",
    "float": "float",
    "bool": "bool",
    "bytes": "bytes",
}


class TypeResolver:
    """Resolves AST type annotations to ResolvedType structures.

    This handles the complex task of converting Python type annotations
    (which may include forward references, Self, Union types, etc.)
    into a normalized ResolvedType that can be used for registration.
    """

    def __init__(
        self,
        namespace: StubNamespace,
        declared_objects: set[str] | None = None,
        declared_enums: set[str] | None = None,
        declared_interfaces: set[str] | None = None,
    ):
        """Initialize the resolver.

        Args:
            namespace: The namespace for evaluating annotations.
            declared_objects: Names of @object_type classes in this module.
            declared_enums: Names of @enum_type enums in this module.
            declared_interfaces: Names of @interface classes in this module.
        """
        self.namespace = namespace
        self.declared_objects = declared_objects or set()
        self.declared_enums = declared_enums or set()
        self.declared_interfaces = declared_interfaces or set()
        self._current_class: str | None = None

    def set_current_class(self, class_name: str | None) -> None:
        """Set the current class context for Self resolution.

        Prefer ``in_class`` which scopes the change and restores the
        previous value automatically.
        """
        self._current_class = class_name

    @contextlib.contextmanager
    def in_class(self, class_name: str | None) -> Iterator[None]:
        """Scope ``Self`` resolution to a class for the duration of the block.

        Save-and-restore keeps nested or recursive usage safe and guarantees
        the resolver returns to its prior state even if the body raises.
        """
        previous = self._current_class
        self._current_class = class_name
        try:
            yield
        finally:
            self._current_class = previous

    def resolve(
        self,
        annotation: ast.expr | str | Any,
        *,
        location: LocationMetadata | None = None,
    ) -> ResolvedType:
        """Resolve a type annotation to a ResolvedType.

        Args:
            annotation: AST expression, string, or evaluated type.
            location: Source location for error messages.

        Returns
        -------
            A ResolvedType representing the annotation.
        """
        try:
            return self._resolve_impl(annotation, location)
        except TypeResolutionError:
            raise
        except Exception as e:
            if isinstance(annotation, ast.expr):
                annotation_str = ast.unparse(annotation)
            else:
                annotation_str = str(annotation)
            msg = f"Failed to resolve type: {e}"
            raise TypeResolutionError(
                msg,
                annotation=annotation_str,
                location=location,
            ) from e

    def _resolve_impl(
        self,
        annotation: ast.expr | str | Any,
        location: LocationMetadata | None,
    ) -> ResolvedType:
        """Internal implementation of resolve."""
        # Handle AST nodes
        if isinstance(annotation, ast.expr):
            return self._resolve_ast(annotation, location)

        # Handle string annotations (from __future__ annotations)
        if isinstance(annotation, str):
            # Try to parse as AST
            try:
                node = ast.parse(annotation, mode="eval").body
                return self._resolve_ast(node, location)
            except SyntaxError:
                # Try to evaluate in namespace
                evaled = self.namespace.eval_annotation(annotation, location=location)
                return self._resolve_evaluated(evaled, location)

        # Handle already-evaluated types
        return self._resolve_evaluated(annotation, location)

    def _resolve_ast(  # noqa: PLR0911
        self,
        node: ast.expr,
        location: LocationMetadata | None,
    ) -> ResolvedType:
        """Resolve an AST node to ResolvedType."""
        # Handle Annotated - unwrap and resolve base type
        unwrapped = unwrap_annotated(node)
        if unwrapped is not node:
            return self._resolve_ast(unwrapped, location)

        # Handle None
        if isinstance(node, ast.Constant) and node.value is None:
            return ResolvedType(kind="void", name="None", is_optional=True)

        # Handle simple names
        if isinstance(node, ast.Name):
            return self._resolve_name(node.id, location)

        # Handle attribute access (e.g., typing.Self, dagger.Container)
        if isinstance(node, ast.Attribute):
            return self._resolve_attribute(node, location)

        # Handle subscripts (generics like list[str], Optional[T])
        if isinstance(node, ast.Subscript):
            return self._resolve_subscript(node, location)

        # Handle binary operations (T | None)
        if isinstance(node, ast.BinOp) and isinstance(node.op, ast.BitOr):
            return self._resolve_union(node, location)

        # Handle string constant (forward reference)
        if isinstance(node, ast.Constant) and isinstance(node.value, str):
            return self._resolve_name(node.value, location)

        # Fallback: try to evaluate
        try:
            evaled = self.namespace.eval_annotation(node, location=location)
            return self._resolve_evaluated(evaled, location)
        except TypeResolutionError:
            annotation_str = ast.unparse(node)
            msg = "Unable to resolve type annotation"
            raise TypeResolutionError(
                msg, annotation=annotation_str, location=location
            ) from None

    def _resolve_name(  # noqa: PLR0911, PLR0912, C901
        self,
        name: str,
        location: LocationMetadata | None,
    ) -> ResolvedType:
        """Resolve a simple name to ResolvedType."""
        # Check for None/void
        if name in ("None", "NoneType"):
            return ResolvedType(kind="void", name="None")

        # Check for Self
        if name == "Self":
            if self._current_class:
                if self._current_class in self.declared_interfaces:
                    kind = "interface"
                else:
                    kind = "object"
                return ResolvedType(
                    kind=kind,
                    name=self._current_class,
                    is_self=True,
                )
            msg = "Self used outside of class context"
            raise TypeResolutionError(msg, annotation=name, location=location)

        # Check for primitives
        if name in PRIMITIVE_TYPES:
            return ResolvedType(kind="primitive", name=name)

        # Check for module-declared types
        if name in self.declared_objects:
            return ResolvedType(kind="object", name=name)
        if name in self.declared_enums:
            return ResolvedType(kind="enum", name=name)
        if name in self.declared_interfaces:
            return ResolvedType(kind="interface", name=name)

        # Check for dagger types
        if name in DAGGER_OBJECT_TYPES:
            return ResolvedType(kind="object", name=name)
        if name in DAGGER_SCALAR_TYPES:
            return ResolvedType(kind="scalar", name=name)
        if name in DAGGER_ENUM_TYPES:
            return ResolvedType(kind="enum", name=name)

        # Try to resolve from namespace
        try:
            evaled = self.namespace.eval_annotation(name, location=location)
            return self._resolve_evaluated(evaled, location)
        except TypeResolutionError:
            # Unknown type - might be a forward reference or a user typo.
            # Keep the permissive fallback so forward refs still work, but
            # warn so that typos don't silently produce garbage TypeDefs.
            logger.warning(
                "Unresolved type %r%s; assuming it is an object type. If "
                "this is a typo or a missing import, the engine will fail "
                "at runtime.",
                name,
                f" at {location.file}:{location.line}" if location else "",
            )
            return ResolvedType(kind="object", name=name)

    def _resolve_attribute(
        self,
        node: ast.Attribute,
        location: LocationMetadata | None,
    ) -> ResolvedType:
        """Resolve an attribute access like typing.Self or dagger.Container."""
        attr_name = node.attr

        # Handle typing.Self or typing_extensions.Self
        if attr_name == "Self":
            if self._current_class:
                if self._current_class in self.declared_interfaces:
                    kind = "interface"
                else:
                    kind = "object"
                return ResolvedType(
                    kind=kind,
                    name=self._current_class,
                    is_self=True,
                )
            msg = "Self used outside of class context"
            raise TypeResolutionError(
                msg, annotation=ast.unparse(node), location=location
            )

        # Handle dagger.X
        if isinstance(node.value, ast.Name) and node.value.id == "dagger":
            if attr_name in DAGGER_OBJECT_TYPES:
                return ResolvedType(kind="object", name=attr_name)
            if attr_name in DAGGER_SCALAR_TYPES:
                return ResolvedType(kind="scalar", name=attr_name)
            if attr_name in DAGGER_ENUM_TYPES:
                return ResolvedType(kind="enum", name=attr_name)

        # Try to resolve by attribute name
        return self._resolve_name(attr_name, location)

    def _resolve_subscript(  # noqa: PLR0911
        self,
        node: ast.Subscript,
        location: LocationMetadata | None,
    ) -> ResolvedType:
        """Resolve a subscript like list[str], Optional[T], Union[A, B]."""
        slice_val = node.slice

        # Get the base type name
        base_name = self._get_subscript_base_name(node, location)
        if base_name is None:
            evaled = self.namespace.eval_annotation(node, location=location)
            return self._resolve_evaluated(evaled, location)

        if base_name in ("list", "List", "Sequence", "tuple", "Tuple"):
            return self._resolve_sequence_subscript(base_name, slice_val, location)

        if base_name == "Optional":
            inner_type = self._get_single_subscript_arg(slice_val, location)
            inner_type.is_optional = True
            return inner_type

        if base_name == "Union":
            return self._resolve_union_subscript(slice_val, location)

        if (
            base_name == "Annotated"
            and isinstance(slice_val, ast.Tuple)
            and slice_val.elts
        ):
            return self._resolve_ast(slice_val.elts[0], location)

        # Unknown generic - try to evaluate
        try:
            evaled = self.namespace.eval_annotation(node, location=location)
            return self._resolve_evaluated(evaled, location)
        except TypeResolutionError:
            logger.warning(
                "Unsupported generic type %r%s; element types are lost. "
                "Dagger supports list[T], Optional[T] and Union[T, None].",
                ast.unparse(node),
                f" at {location.file}:{location.line}" if location else "",
            )
            return ResolvedType(kind="object", name=base_name)

    def _get_subscript_base_name(
        self,
        node: ast.Subscript,
        location: LocationMetadata | None,
    ) -> str | None:
        """Extract the base name from a subscript, or None if unresolvable."""
        value = node.value
        if isinstance(value, ast.Name):
            return value.id
        if isinstance(value, ast.Attribute):
            return value.attr
        # Try to evaluate; raise if that also fails
        try:
            self.namespace.eval_annotation(node, location=location)
        except TypeResolutionError:
            annotation_str = ast.unparse(node)
            msg = "Unable to resolve subscripted type"
            raise TypeResolutionError(
                msg, annotation=annotation_str, location=location
            ) from None
        return None

    def _resolve_sequence_subscript(
        self,
        base_name: str,
        slice_val: ast.expr,
        location: LocationMetadata | None,
    ) -> ResolvedType:
        """Resolve list[T], tuple[T, ...], or Sequence[T]."""
        if base_name in ("tuple", "Tuple") and isinstance(slice_val, ast.Tuple):
            non_ellipsis = [
                el
                for el in slice_val.elts
                if not (isinstance(el, ast.Constant) and el.value is ...)
            ]
            if non_ellipsis:
                element_type = self._resolve_ast(non_ellipsis[0], location)
            else:
                element_type = ResolvedType(kind="primitive", name="Any")
        else:
            element_type = self._get_single_subscript_arg(slice_val, location)
        return ResolvedType(
            kind="list",
            name="list",
            element_type=element_type,
        )

    def _resolve_union(
        self,
        node: ast.BinOp,
        location: LocationMetadata | None,
    ) -> ResolvedType:
        """Resolve a union type like T | None."""
        left = self._resolve_ast(node.left, location)
        right = self._resolve_ast(node.right, location)

        # Handle T | None -> make T optional
        if right.kind == "void" or right.name == "None":
            left.is_optional = True
            return left
        if left.kind == "void" or left.name == "None":
            right.is_optional = True
            return right

        # General union - not supported in Dagger
        msg = "Union types (other than T | None) are not supported in Dagger"
        raise TypeResolutionError(msg, annotation=ast.unparse(node), location=location)

    def _resolve_union_subscript(
        self,
        slice_val: ast.expr,
        location: LocationMetadata | None,
    ) -> ResolvedType:
        """Resolve Union[A, B, ...] subscript."""
        args = slice_val.elts if isinstance(slice_val, ast.Tuple) else [slice_val]

        non_none_types = []
        has_none = False

        for arg in args:
            is_none_const = isinstance(arg, ast.Constant) and arg.value is None
            is_none_name = isinstance(arg, ast.Name) and arg.id in ("None", "NoneType")
            if is_none_const or is_none_name:
                has_none = True
            else:
                non_none_types.append(arg)

        if not non_none_types:
            return ResolvedType(kind="void", name="None")

        if len(non_none_types) == 1:
            resolved = self._resolve_ast(non_none_types[0], location)
            resolved.is_optional = has_none
            return resolved

        # Multiple non-None types - not supported
        msg = "Union types (other than Optional[T]) are not supported in Dagger"
        annotation_str = f"Union[{', '.join(ast.unparse(t) for t in args)}]"
        raise TypeResolutionError(msg, annotation=annotation_str, location=location)

    def _get_single_subscript_arg(
        self,
        slice_val: ast.expr,
        location: LocationMetadata | None,
    ) -> ResolvedType:
        """Get the single type argument from a subscript like list[T]."""
        if isinstance(slice_val, ast.Tuple) and slice_val.elts:
            return self._resolve_ast(slice_val.elts[0], location)
        return self._resolve_ast(slice_val, location)

    def _resolve_evaluated(  # noqa: PLR0911, PLR0912, C901
        self,
        t: Any,
        location: LocationMetadata | None,
    ) -> ResolvedType:
        """Resolve an already-evaluated type."""
        # Handle None
        if t is None or t is type(None):
            return ResolvedType(kind="void", name="None")

        # Handle Self
        if t is typing_extensions.Self:
            if self._current_class:
                if self._current_class in self.declared_interfaces:
                    kind = "interface"
                else:
                    kind = "object"
                return ResolvedType(
                    kind=kind,
                    name=self._current_class,
                    is_self=True,
                )
            msg = "Self used outside of class context"
            raise TypeResolutionError(msg, location=location)

        # Handle stub types
        if is_stub_type(t):
            return ResolvedType(kind="object", name=t.__name__)

        # Handle primitives
        if t in (str, int, float, bool, bytes):
            return ResolvedType(kind="primitive", name=t.__name__)

        # Handle new-style unions (Python 3.10+)
        if isinstance(t, types.UnionType):
            return self._resolve_union_type(t, location)

        # Handle typing special forms
        origin = get_origin(t)
        args = get_args(t)

        if origin is not None:
            # Handle Union
            if origin is typing.Union:
                return self._resolve_union_args(args, location)

            # Handle list, Sequence, Iterable (typing.Sequence[T] yields
            # collections.abc.Sequence as its origin, not typing.Sequence).
            if origin in (
                list,
                collections.abc.Sequence,
                collections.abc.Iterable,
            ):
                if args:
                    element_type = self._resolve_evaluated(args[0], location)
                    return ResolvedType(
                        kind="list",
                        name="list",
                        element_type=element_type,
                    )
                return ResolvedType(kind="list", name="list")

            # Handle Annotated
            if origin is typing.Annotated and args:
                return self._resolve_evaluated(args[0], location)

        # Handle classes
        if isinstance(t, type):
            name = t.__name__

            # Check module-declared types
            if name in self.declared_objects:
                return ResolvedType(kind="object", name=name)
            if name in self.declared_enums:
                return ResolvedType(kind="enum", name=name)
            if name in self.declared_interfaces:
                return ResolvedType(kind="interface", name=name)

            # Check dagger types
            if name in DAGGER_OBJECT_TYPES:
                return ResolvedType(kind="object", name=name)
            if name in DAGGER_SCALAR_TYPES:
                return ResolvedType(kind="scalar", name=name)
            if name in DAGGER_ENUM_TYPES:
                return ResolvedType(kind="enum", name=name)

            # Check if it's an enum subclass (e.g., dependency enums)
            if issubclass(t, enum.Enum):
                return ResolvedType(kind="enum", name=name)

            # Assume it's an object type
            return ResolvedType(kind="object", name=name)

        # Fallback
        return ResolvedType(kind="object", name=str(t))

    def _resolve_union_type(
        self,
        t: types.UnionType,
        location: LocationMetadata | None,
    ) -> ResolvedType:
        """Resolve a Python 3.10+ union type."""
        args = t.__args__
        return self._resolve_union_args(args, location)

    def _resolve_union_args(
        self,
        args: tuple,
        location: LocationMetadata | None,
    ) -> ResolvedType:
        """Resolve union arguments."""
        non_none_types = []
        has_none = False

        for arg in args:
            if arg is None or arg is type(None):
                has_none = True
            else:
                non_none_types.append(arg)

        if not non_none_types:
            return ResolvedType(kind="void", name="None")

        if len(non_none_types) == 1:
            resolved = self._resolve_evaluated(non_none_types[0], location)
            resolved.is_optional = has_none
            return resolved

        # Multiple non-None types - not supported
        msg = f"Union types (other than Optional[T]) are not supported: Union[{args}]"
        raise TypeResolutionError(msg, location=location)
