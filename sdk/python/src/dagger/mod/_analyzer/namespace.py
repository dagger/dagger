"""Stub namespace for evaluating type annotations.

This module provides a safe namespace for evaluating stringified
type annotations (from `from __future__ import annotations`) without
requiring all imports to be available.
"""

from __future__ import annotations

import ast
import typing
from typing import Any

import typing_extensions

from dagger.mod._analyzer.errors import TypeResolutionError
from dagger.mod._analyzer.metadata import LocationMetadata


class StubType:
    """A stub type for unavailable imports.

    This allows type annotations to be parsed even when the actual
    type is not importable.
    """

    def __init__(self, name: str, module: str | None = None):
        self.__name__ = name
        self.__module__ = module or "stub"
        self._is_stub = True

    def __repr__(self) -> str:
        if self.__module__ and self.__module__ != "stub":
            return f"<StubType {self.__module__}.{self.__name__}>"
        return f"<StubType {self.__name__}>"

    def __getitem__(self, item):
        """Support generic subscripting like StubType[T]."""
        return self

    def __or__(self, other):
        """Support union syntax like StubType | None."""
        return typing.Union[self, other]  # noqa: UP007

    def __ror__(self, other):
        """Support reverse union syntax like None | StubType."""
        return typing.Union[other, self]  # noqa: UP007


def is_stub_type(t: Any) -> bool:
    """Check if a type is a stub type."""
    return getattr(t, "_is_stub", False)


class StubNamespace:
    """A namespace for evaluating type annotations.

    Provides built-in types, typing module types, and stubs for
    unavailable imports.
    """

    def __init__(self):
        self._namespace: dict[str, Any] = self._build_base_namespace()
        self._declared_types: set[str] = set()

    def _build_base_namespace(self) -> dict[str, Any]:
        """Build the base namespace with built-in and typing types."""
        ns: dict[str, Any] = {
            # Built-in types
            "str": str,
            "int": int,
            "float": float,
            "bool": bool,
            "bytes": bytes,
            "None": type(None),
            "type": type,
            "list": list,
            "dict": dict,
            "tuple": tuple,
            "set": set,
            "frozenset": frozenset,
            # typing module - commonly used
            "Any": typing.Any,
            "Union": typing.Union,
            "Optional": typing.Optional,
            "List": list,
            "Dict": dict,
            "Tuple": tuple,
            "Set": set,
            "FrozenSet": frozenset,
            "Sequence": typing.Sequence,
            "Mapping": typing.Mapping,
            "Iterable": typing.Iterable,
            "Iterator": typing.Iterator,
            "Callable": typing.Callable,
            "Type": type,
            "ClassVar": typing.ClassVar,
            "Final": typing.Final,
            "Literal": typing.Literal,
            "TypeVar": typing.TypeVar,
            "Generic": typing.Generic,
            "Protocol": typing.Protocol,
            "Annotated": typing.Annotated,
            # typing_extensions
            "Self": typing_extensions.Self,
            "Doc": typing_extensions.Doc,
            # Make typing module available
            "typing": typing,
            "typing_extensions": typing_extensions,
        }

        # Add dagger types
        try:
            import dagger
            from dagger.mod._arguments import (
                DefaultAddress,
                DefaultPath,
                Deprecated,
                Ignore,
                Name,
            )

            ns["dagger"] = dagger
            ns["DefaultPath"] = DefaultPath
            ns["DefaultAddress"] = DefaultAddress
            ns["Ignore"] = Ignore
            ns["Name"] = Name
            ns["Deprecated"] = Deprecated

            # Add common dagger types directly
            for name in [
                "Container",
                "Directory",
                "File",
                "Secret",
                "Service",
                "CacheVolume",
                "Socket",
                "ModuleSource",
                "Module",
                "Platform",
                "JSON",
                "GitRepository",
                "GitRef",
            ]:
                if hasattr(dagger, name):
                    ns[name] = getattr(dagger, name)

        except ImportError:
            # dagger module not available, create stubs
            ns["dagger"] = StubType("dagger")

        return ns

    def add_import(self, name: str, alias: str | None = None) -> None:
        """Add an import to the namespace.

        Args:
            name: The module or attribute name to import.
            alias: Optional alias for the import.
        """
        key = alias or name.rsplit(".", maxsplit=1)[-1]

        # Try to actually import it
        try:
            import importlib

            parts = name.split(".")
            if len(parts) == 1:
                module = importlib.import_module(name)
                self._namespace[key] = module
            else:
                # Handle dotted imports (e.g., "foo.bar")
                module_name = ".".join(parts[:-1])
                attr_name = parts[-1]
                module = importlib.import_module(module_name)
                self._namespace[key] = getattr(module, attr_name)
        except (ImportError, AttributeError):
            # Create a stub type for unavailable imports
            self._namespace[key] = StubType(key, name)

    def add_from_import(self, module: str, name: str, alias: str | None = None) -> None:
        """Add a from-import to the namespace.

        Args:
            module: The module to import from.
            name: The name to import.
            alias: Optional alias for the import.
        """
        key = alias or name

        # Try to actually import it
        try:
            import importlib

            mod = importlib.import_module(module)
            self._namespace[key] = getattr(mod, name)
        except (ImportError, AttributeError):
            # Create a stub type
            self._namespace[key] = StubType(name, module)

    def add_declared_type(self, name: str) -> None:
        """Register a type declared in the module being analyzed.

        This creates a forward reference placeholder.
        """
        self._declared_types.add(name)
        # Create a stub that can be used in annotations
        self._namespace[name] = StubType(name, "module")

    def get_namespace(self) -> dict[str, Any]:
        """Get the complete namespace for evaluation."""
        return self._namespace.copy()

    def eval_annotation(
        self,
        annotation: str | ast.expr,
        *,
        location: LocationMetadata | None = None,
    ) -> Any:
        """Evaluate a type annotation in this namespace.

        Args:
            annotation: The annotation string or AST node.
            location: Source location for error messages.

        Returns
        -------
            The evaluated type.

        Raises
        ------
            TypeResolutionError: If the annotation cannot be evaluated.
        """
        if isinstance(annotation, ast.expr):
            annotation_str = ast.unparse(annotation)
        else:
            annotation_str = annotation

        try:
            # Use eval with our controlled namespace
            return eval(annotation_str, {"__builtins__": {}}, self._namespace)  # noqa: S307
        except Exception as e:
            msg = f"Failed to evaluate type annotation: {e}"
            raise TypeResolutionError(
                msg,
                annotation=annotation_str,
                location=location,
            ) from e


def extract_imports_from_ast(tree: ast.Module) -> list[tuple[str, str, str | None]]:
    """Extract import statements from an AST.

    Returns
    -------
        List of tuples: (import_type, name, alias)
        - import_type: "import" or "from"
        - name: full dotted name or "module.name" for from imports
        - alias: optional alias
    """
    imports: list[tuple[str, str, str | None]] = []

    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            imports.extend(("import", alias.name, alias.asname) for alias in node.names)
        elif isinstance(node, ast.ImportFrom):
            module = node.module or ""
            imports.extend(
                (
                    "from",
                    f"{module}.{alias.name}" if module else alias.name,
                    alias.asname,
                )
                for alias in node.names
                if alias.name != "*"
            )

    return imports


def build_namespace_from_ast(tree: ast.Module) -> StubNamespace:
    """Build a namespace from an AST module.

    This extracts imports and creates appropriate stubs for
    unavailable modules.
    """
    namespace = StubNamespace()

    # Extract and add imports
    for import_type, name, alias in extract_imports_from_ast(tree):
        if import_type == "import":
            namespace.add_import(name, alias)
        else:
            # from X.Y import Z - name is "X.Y.Z"
            parts = name.rsplit(".", 1)
            if "." in name:
                module, attr = parts
                namespace.add_from_import(module, attr, alias)
            else:
                namespace.add_import(name, alias)

    # Find declared classes to use as forward references
    for node in ast.walk(tree):
        if isinstance(node, ast.ClassDef):
            namespace.add_declared_type(node.name)

    return namespace
