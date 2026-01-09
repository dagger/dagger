"""AST visitor for extracting Dagger module definitions.

This module provides the core AST parsing logic to extract decorated classes,
functions, and fields from Python source code without executing it.
"""

from __future__ import annotations

import ast
from pathlib import Path
from typing import Any

from dagger.mod._ir import (
    EnumMemberIR,
    FieldIR,
    FunctionIR,
    ObjectTypeIR,
    ParameterIR,
    SourceLocation,
    TypeAnnotation,
)
from dagger.mod._utils import normalize_name, to_camel_case

# Decorator names recognized by the analyzer
OBJECT_TYPE_DECORATORS = frozenset({"object_type"})
INTERFACE_DECORATORS = frozenset({"interface"})
ENUM_DECORATORS = frozenset({"enum_type"})
FUNCTION_DECORATORS = frozenset({"function"})
CHECK_DECORATORS = frozenset({"check"})
FIELD_FUNCTION = "field"


def _get_decorator_name(dec: ast.expr) -> str | None:
    """Extract decorator name from AST node.

    Handles:
    - @decorator
    - @module.decorator
    - @decorator(args)
    - @module.decorator(args)
    """
    if isinstance(dec, ast.Name):
        return dec.id
    if isinstance(dec, ast.Attribute):
        return dec.attr
    if isinstance(dec, ast.Call):
        return _get_decorator_name(dec.func)
    return None


def _has_decorator(
    node: ast.ClassDef | ast.FunctionDef | ast.AsyncFunctionDef, names: frozenset[str]
) -> bool:
    """Check if node has any of the specified decorators."""
    return any(_get_decorator_name(dec) in names for dec in node.decorator_list)


def _get_decorator_kwargs(dec: ast.expr) -> dict[str, Any]:
    """Extract keyword arguments from a decorator call."""
    if not isinstance(dec, ast.Call):
        return {}

    result: dict[str, Any] = {}
    for kw in dec.keywords:
        if kw.arg is None:
            continue
        # Extract simple constant values
        if isinstance(kw.value, ast.Constant):
            result[kw.arg] = kw.value.value
        elif isinstance(kw.value, (ast.List, ast.Tuple)):
            # Handle list/tuple of constants
            elements = []
            for elt in kw.value.elts:
                if isinstance(elt, ast.Constant):
                    elements.append(elt.value)
            result[kw.arg] = elements
    return result


def _find_decorator(
    node: ast.ClassDef | ast.FunctionDef | ast.AsyncFunctionDef, names: frozenset[str]
) -> ast.expr | None:
    """Find the first decorator matching the given names."""
    for dec in node.decorator_list:
        if _get_decorator_name(dec) in names:
            return dec
    return None


class TypeAnnotationParser:
    """Parses type annotations from AST nodes.

    Extracts type structure (optional, list, element type) and metadata
    from Annotated[] types (Doc, Name, DefaultPath, Ignore, Deprecated).
    """

    def __init__(self, source_file: Path):
        self.source_file = source_file

    def parse(self, node: ast.expr | None, location: SourceLocation) -> TypeAnnotation:
        """Parse a type annotation AST node into TypeAnnotation IR."""
        if node is None:
            return TypeAnnotation(raw="Any", location=location)

        raw = ast.unparse(node)

        # Extract metadata from Annotated[] first
        doc, name, default_path, ignore, deprecated = self._extract_annotated_metadata(
            node
        )

        # Strip Annotated wrapper for type analysis
        inner_node = self._strip_annotated(node)

        # Analyze type structure
        is_optional = self._is_optional_type(inner_node)
        is_list, element_type = self._extract_list_type(inner_node)

        return TypeAnnotation(
            raw=raw,
            location=location,
            is_optional=is_optional,
            is_list=is_list,
            element_type=element_type,
            doc=doc,
            name=name,
            default_path=default_path,
            ignore=tuple(ignore) if ignore else None,
            deprecated=deprecated,
        )

    def _strip_annotated(self, node: ast.expr) -> ast.expr:
        """Remove Annotated[] wrapper to get the base type."""
        if isinstance(node, ast.Subscript) and self._is_annotated(node.value):
            # Annotated[T, ...] -> T
            if isinstance(node.slice, ast.Tuple) and node.slice.elts:
                return node.slice.elts[0]
        return node

    def _is_annotated(self, node: ast.expr) -> bool:
        """Check if node is Annotated type."""
        if isinstance(node, ast.Name):
            return node.id == "Annotated"
        if isinstance(node, ast.Attribute):
            return node.attr == "Annotated"
        return False

    def _is_optional_type(self, node: ast.expr) -> bool:
        """Check if type is Optional or Union with None."""
        # Handle X | None (Python 3.10+ union syntax)
        if isinstance(node, ast.BinOp) and isinstance(node.op, ast.BitOr):
            return self._contains_none(node)

        # Handle Optional[X] or Union[X, None]
        if isinstance(node, ast.Subscript):
            if isinstance(node.value, ast.Name):
                if node.value.id == "Optional":
                    return True
                if node.value.id == "Union":
                    return self._union_contains_none(node.slice)
            if isinstance(node.value, ast.Attribute):
                if node.value.attr in ("Optional", "Union"):
                    if node.value.attr == "Optional":
                        return True
                    return self._union_contains_none(node.slice)

        return False

    def _contains_none(self, node: ast.expr) -> bool:
        """Check if a binary union contains None."""
        if isinstance(node, ast.Constant) and node.value is None:
            return True
        if isinstance(node, ast.BinOp) and isinstance(node.op, ast.BitOr):
            return self._contains_none(node.left) or self._contains_none(node.right)
        return False

    def _union_contains_none(self, slice_node: ast.expr) -> bool:
        """Check if Union[] type args contain None."""
        if isinstance(slice_node, ast.Tuple):
            return any(
                isinstance(elt, ast.Constant) and elt.value is None
                for elt in slice_node.elts
            )
        return isinstance(slice_node, ast.Constant) and slice_node.value is None

    def _extract_list_type(self, node: ast.expr) -> tuple[bool, str | None]:
        """Extract list/sequence type and element type."""
        # Strip Optional wrapper first
        inner = self._unwrap_optional(node)

        if isinstance(inner, ast.Subscript):
            type_name = None
            if isinstance(inner.value, ast.Name):
                type_name = inner.value.id
            elif isinstance(inner.value, ast.Attribute):
                type_name = inner.value.attr

            if type_name in ("list", "List", "Sequence"):
                element = ast.unparse(inner.slice)
                return True, element

        return False, None

    def _unwrap_optional(self, node: ast.expr) -> ast.expr:
        """Unwrap Optional[T] to T."""
        if isinstance(node, ast.BinOp) and isinstance(node.op, ast.BitOr):
            # X | None -> X
            if isinstance(node.right, ast.Constant) and node.right.value is None:
                return node.left
            if isinstance(node.left, ast.Constant) and node.left.value is None:
                return node.right

        if isinstance(node, ast.Subscript):
            if isinstance(node.value, ast.Name) and node.value.id == "Optional":
                return node.slice
            if isinstance(node.value, ast.Name) and node.value.id == "Union":
                if isinstance(node.slice, ast.Tuple):
                    # Return first non-None type
                    for elt in node.slice.elts:
                        if not (isinstance(elt, ast.Constant) and elt.value is None):
                            return elt

        return node

    def _extract_annotated_metadata(
        self, node: ast.expr
    ) -> tuple[str | None, str | None, str | None, list[str] | None, str | None]:
        """Extract metadata from Annotated[] type.

        Returns (doc, name, default_path, ignore, deprecated)
        """
        if not isinstance(node, ast.Subscript):
            return None, None, None, None, None

        if not self._is_annotated(node.value):
            return None, None, None, None, None

        if not isinstance(node.slice, ast.Tuple):
            return None, None, None, None, None

        doc = None
        name = None
        default_path = None
        ignore = None
        deprecated = None

        # Skip first element (the actual type), iterate metadata
        for arg in node.slice.elts[1:]:
            if isinstance(arg, ast.Call):
                call_name = self._get_call_name(arg)

                if call_name == "Doc" and arg.args:
                    if isinstance(arg.args[0], ast.Constant):
                        doc = arg.args[0].value

                elif call_name == "Name" and arg.args:
                    if isinstance(arg.args[0], ast.Constant):
                        name = arg.args[0].value

                elif call_name == "DefaultPath" and arg.args:
                    if isinstance(arg.args[0], ast.Constant):
                        default_path = arg.args[0].value

                elif call_name == "Ignore" and arg.args:
                    if isinstance(arg.args[0], (ast.List, ast.Tuple)):
                        ignore = [
                            elt.value
                            for elt in arg.args[0].elts
                            if isinstance(elt, ast.Constant)
                        ]

                elif call_name == "Deprecated":
                    if arg.args and isinstance(arg.args[0], ast.Constant):
                        deprecated = arg.args[0].value
                    elif not arg.args:
                        deprecated = ""

        return doc, name, default_path, ignore, deprecated

    def _get_call_name(self, node: ast.Call) -> str | None:
        """Get the name of a function call."""
        if isinstance(node.func, ast.Name):
            return node.func.id
        if isinstance(node.func, ast.Attribute):
            return node.func.attr
        return None


class DaggerModuleVisitor(ast.NodeVisitor):
    """AST visitor that extracts Dagger module definitions.

    Visits class definitions and extracts decorated types (objects, interfaces,
    enums) along with their fields and functions.
    """

    def __init__(self, source_file: Path, source_code: str):
        self.source_file = source_file
        self.source_code = source_code
        self.source_lines = source_code.splitlines()

        self.objects: list[ObjectTypeIR] = []
        self.annotation_parser = TypeAnnotationParser(source_file)

    def visit_ClassDef(self, node: ast.ClassDef) -> None:
        """Visit class definition and extract if decorated."""
        is_object = _has_decorator(node, OBJECT_TYPE_DECORATORS)
        is_interface = _has_decorator(node, INTERFACE_DECORATORS)
        is_enum = _has_decorator(node, ENUM_DECORATORS)

        if is_object or is_interface or is_enum:
            obj_ir = self._extract_object_type(
                node,
                is_interface=is_interface,
                is_enum=is_enum,
            )
            self.objects.append(obj_ir)

    def _make_location(self, node: ast.AST) -> SourceLocation:
        """Create source location from AST node."""
        return SourceLocation(
            file=self.source_file,
            line=getattr(node, "lineno", 0),
            column=getattr(node, "col_offset", 0),
        )

    def _extract_docstring(
        self, node: ast.ClassDef | ast.FunctionDef | ast.AsyncFunctionDef
    ) -> str | None:
        """Extract docstring from class or function."""
        return ast.get_docstring(node)

    def _extract_object_type(
        self,
        node: ast.ClassDef,
        *,
        is_interface: bool,
        is_enum: bool,
    ) -> ObjectTypeIR:
        """Extract object type from class definition."""
        location = self._make_location(node)

        # Extract deprecated from decorator
        deprecated = None
        if is_interface:
            dec = _find_decorator(node, INTERFACE_DECORATORS)
        elif is_enum:
            dec = _find_decorator(node, ENUM_DECORATORS)
        else:
            dec = _find_decorator(node, OBJECT_TYPE_DECORATORS)
        if dec:
            kwargs = _get_decorator_kwargs(dec)
            deprecated = kwargs.get("deprecated")

        doc = self._extract_docstring(node)

        if is_enum:
            members = self._extract_enum_members(node)
            return ObjectTypeIR(
                name=node.name,
                qualified_name=f"{self.source_file.stem}.{node.name}",
                location=location,
                module_path=self.source_file,
                is_enum=True,
                enum_members=tuple(members),
                doc=doc,
                deprecated=deprecated,
            )

        fields = self._extract_fields(node)
        functions = self._extract_functions(node)

        return ObjectTypeIR(
            name=node.name,
            qualified_name=f"{self.source_file.stem}.{node.name}",
            location=location,
            module_path=self.source_file,
            is_interface=is_interface,
            fields=tuple(fields),
            functions=tuple(functions),
            doc=doc,
            deprecated=deprecated,
        )

    def _extract_fields(self, class_node: ast.ClassDef) -> list[FieldIR]:
        """Extract fields from class body."""
        fields: list[FieldIR] = []

        for stmt in class_node.body:
            if not isinstance(stmt, ast.AnnAssign):
                continue
            if not isinstance(stmt.target, ast.Name):
                continue

            # Check if it's a field() call
            if stmt.value is None:
                continue
            if not isinstance(stmt.value, ast.Call):
                continue

            call_name = None
            if isinstance(stmt.value.func, ast.Name):
                call_name = stmt.value.func.id
            elif isinstance(stmt.value.func, ast.Attribute):
                call_name = stmt.value.func.attr

            if call_name != FIELD_FUNCTION:
                continue

            field_ir = self._extract_field(stmt)
            if field_ir:
                fields.append(field_ir)

        return fields

    def _extract_field(self, node: ast.AnnAssign) -> FieldIR | None:
        """Extract a single field from annotated assignment."""
        if not isinstance(node.target, ast.Name):
            return None

        python_name = node.target.id
        location = self._make_location(node)
        annotation = self.annotation_parser.parse(node.annotation, location)

        # Parse field() call arguments
        deprecated = None
        has_default = False
        default_value = None
        default_repr = None
        init = True

        if isinstance(node.value, ast.Call):
            for kw in node.value.keywords:
                if kw.arg == "deprecated" and isinstance(kw.value, ast.Constant):
                    deprecated = kw.value.value
                elif kw.arg == "default":
                    has_default = True
                    if isinstance(kw.value, ast.Constant):
                        default_value = kw.value.value
                    default_repr = ast.unparse(kw.value)
                elif kw.arg == "default_factory":
                    has_default = True
                    default_repr = f"{ast.unparse(kw.value)}()"
                elif kw.arg == "init" and isinstance(kw.value, ast.Constant):
                    init = kw.value.value
                elif kw.arg == "name" and isinstance(kw.value, ast.Constant):
                    # Override API name from field() call
                    if annotation.name is None:
                        annotation = TypeAnnotation(
                            raw=annotation.raw,
                            location=annotation.location,
                            is_optional=annotation.is_optional,
                            is_list=annotation.is_list,
                            element_type=annotation.element_type,
                            doc=annotation.doc,
                            name=kw.value.value,
                            default_path=annotation.default_path,
                            ignore=annotation.ignore,
                            deprecated=annotation.deprecated,
                        )

        # Determine API name
        api_name = annotation.name or to_camel_case(normalize_name(python_name))

        return FieldIR(
            python_name=python_name,
            api_name=api_name,
            annotation=annotation,
            location=location,
            has_default=has_default,
            default_value=default_value,
            default_repr=default_repr,
            init=init,
            deprecated=deprecated,
        )

    def _extract_functions(self, class_node: ast.ClassDef) -> list[FunctionIR]:
        """Extract functions from class body."""
        functions: list[FunctionIR] = []

        for stmt in class_node.body:
            if not isinstance(stmt, (ast.FunctionDef, ast.AsyncFunctionDef)):
                continue

            # Check for @function decorator
            if not _has_decorator(stmt, FUNCTION_DECORATORS):
                continue

            func_ir = self._extract_function(stmt)
            if func_ir:
                functions.append(func_ir)

        return functions

    def _extract_function(
        self, node: ast.FunctionDef | ast.AsyncFunctionDef
    ) -> FunctionIR | None:
        """Extract a single function from method definition."""
        location = self._make_location(node)

        # Extract decorator metadata
        is_check = _has_decorator(node, CHECK_DECORATORS)
        cache_policy = None
        deprecated = None
        doc_override = None
        name_override = None

        func_dec = _find_decorator(node, FUNCTION_DECORATORS)
        if func_dec:
            kwargs = _get_decorator_kwargs(func_dec)
            cache_policy = kwargs.get("cache")
            deprecated = kwargs.get("deprecated")
            doc_override = kwargs.get("doc")
            name_override = kwargs.get("name")

        # Extract parameters
        parameters = self._extract_parameters(node)

        # Extract return annotation
        return_annotation = None
        if node.returns:
            return_annotation = self.annotation_parser.parse(
                node.returns, self._make_location(node.returns)
            )

        # Extract docstring
        doc = doc_override or self._extract_docstring(node)

        # Determine API name
        python_name = node.name
        if name_override is not None:
            api_name = name_override
        else:
            api_name = to_camel_case(normalize_name(python_name))

        return FunctionIR(
            python_name=python_name,
            api_name=api_name,
            parameters=tuple(parameters),
            return_annotation=return_annotation,
            location=location,
            doc=doc,
            is_check=is_check,
            cache_policy=cache_policy,
            deprecated=deprecated,
            is_async=isinstance(node, ast.AsyncFunctionDef),
        )

    def _extract_parameters(
        self, func_node: ast.FunctionDef | ast.AsyncFunctionDef
    ) -> list[ParameterIR]:
        """Extract parameters from function definition."""
        params: list[ParameterIR] = []

        # Calculate default value positions
        args = func_node.args
        num_args = len(args.args)
        num_defaults = len(args.defaults)
        first_default_idx = num_args - num_defaults

        for i, arg in enumerate(args.args):
            # Skip 'self' and 'cls'
            if arg.arg in ("self", "cls"):
                continue

            location = self._make_location(arg)
            annotation = self.annotation_parser.parse(arg.annotation, location)

            # Check for default value
            has_default = False
            default_value = None
            default_repr = None

            if i >= first_default_idx:
                has_default = True
                default_node = args.defaults[i - first_default_idx]
                default_repr = ast.unparse(default_node)
                if isinstance(default_node, ast.Constant):
                    default_value = default_node.value

            # Determine API name
            api_name = annotation.name or to_camel_case(normalize_name(arg.arg))

            params.append(
                ParameterIR(
                    python_name=arg.arg,
                    api_name=api_name,
                    annotation=annotation,
                    location=location,
                    has_default=has_default,
                    default_value=default_value,
                    default_repr=default_repr,
                )
            )

        return params

    def _extract_enum_members(self, class_node: ast.ClassDef) -> list[EnumMemberIR]:
        """Extract enum members from class body."""
        members: list[EnumMemberIR] = []

        for i, stmt in enumerate(class_node.body):
            if not isinstance(stmt, ast.Assign):
                continue

            for target in stmt.targets:
                if not isinstance(target, ast.Name):
                    continue

                member_name = target.id

                # Skip private attributes
                if member_name.startswith("_"):
                    continue

                # Get value
                value = ""
                if isinstance(stmt.value, ast.Constant):
                    value = str(stmt.value.value)
                else:
                    value = ast.unparse(stmt.value)

                # Extract docstring from next statement
                doc = self._extract_doc_from_next_stmt(class_node.body, i)

                # Parse deprecated directive from doc
                deprecated = None
                if doc and ".. deprecated::" in doc:
                    parts = doc.split(".. deprecated::", 1)
                    doc = parts[0].strip() or None
                    deprecated = parts[1].strip()

                location = self._make_location(stmt)

                members.append(
                    EnumMemberIR(
                        name=member_name,
                        value=value,
                        doc=doc,
                        deprecated=deprecated,
                        location=location,
                    )
                )

        return members

    def _extract_doc_from_next_stmt(
        self, body: list[ast.stmt], index: int
    ) -> str | None:
        """Extract documentation from statement following an assignment."""
        next_idx = index + 1
        if next_idx >= len(body):
            return None

        next_stmt = body[next_idx]
        if (
            isinstance(next_stmt, ast.Expr)
            and isinstance(next_stmt.value, ast.Constant)
            and isinstance(next_stmt.value.value, str)
        ):
            return next_stmt.value.value.strip()
        return None


def parse_file(source_path: Path) -> list[ObjectTypeIR]:
    """Parse a single Python file and extract Dagger types.

    Args:
        source_path: Path to Python file

    Returns
    -------
        List of extracted ObjectTypeIR

    Raises
    ------
        SyntaxError: If file contains invalid Python syntax
    """
    source_code = source_path.read_text(encoding="utf-8")
    tree = ast.parse(source_code, filename=str(source_path))

    visitor = DaggerModuleVisitor(source_path, source_code)
    visitor.visit(tree)

    return visitor.objects


def get_module_docstring(source_path: Path) -> str | None:
    """Extract module-level docstring from Python file."""
    source_code = source_path.read_text(encoding="utf-8")
    tree = ast.parse(source_code)
    return ast.get_docstring(tree)
