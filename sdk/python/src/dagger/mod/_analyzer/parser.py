"""AST parser for extracting module declarations.

This module parses Python source files and extracts decorated
classes, functions, and fields for Dagger module registration.
"""

from __future__ import annotations

import ast
import json
from pathlib import Path
from typing import Any

from dagger.mod._analyzer.errors import ParseError
from dagger.mod._analyzer.metadata import (
    EnumMemberMetadata,
    EnumTypeMetadata,
    FieldMetadata,
    FunctionMetadata,
    LocationMetadata,
    ObjectTypeMetadata,
    ParameterMetadata,
    ResolvedType,
)
from dagger.mod._analyzer.namespace import StubNamespace
from dagger.mod._analyzer.resolver import TypeResolver
from dagger.mod._analyzer.visitors.annotations import (
    extract_annotated_metadata,
    get_annotation_string,
)
from dagger.mod._analyzer.visitors.decorators import (
    extract_decorator_info,
    find_decorator,
    has_decorator,
    is_classmethod,
)


def normalize_name(name: str) -> str:
    """Normalize a Python name for API usage.

    Removes trailing underscore used for reserved word avoidance.
    """
    is_trailing_underscore = (
        name.endswith("_")
        and len(name) > 1
        and name[-2] != "_"
        and not name.startswith("_")
    )
    if is_trailing_underscore:
        return name[:-1]
    return name


def get_location(node: ast.AST, file_path: str) -> LocationMetadata:
    """Get source location from an AST node."""
    return LocationMetadata(
        file=file_path,
        line=getattr(node, "lineno", 0),
        column=getattr(node, "col_offset", 0),
    )


def get_docstring(
    node: ast.ClassDef | ast.FunctionDef | ast.AsyncFunctionDef | ast.Module,
) -> str | None:
    """Extract docstring from a node."""
    return ast.get_docstring(node)


class ModuleParser:
    """Parser for extracting declarations from Python source files.

    This parser reads Python source, builds an AST, and extracts
    all Dagger-decorated classes, functions, and fields.
    """

    def __init__(self, source_files: list[str | Path], main_object_name: str):
        """Initialize the parser.

        Args:
            source_files: List of Python source files to parse.
            main_object_name: Name of the main object class.
        """
        self.source_files = [Path(f) for f in source_files]
        self.main_object_name = main_object_name

        # Parsed data
        self._asts: dict[Path, ast.Module] = {}
        self._namespace: StubNamespace | None = None
        self._resolver: TypeResolver | None = None

        # Collected declarations
        self._objects: dict[str, ObjectTypeMetadata] = {}
        self._enums: dict[str, EnumTypeMetadata] = {}

        # Track what types are declared (for resolver)
        self._declared_objects: set[str] = set()
        self._declared_enums: set[str] = set()
        self._declared_interfaces: set[str] = set()

    def parse(
        self,
    ) -> tuple[dict[str, ObjectTypeMetadata], dict[str, EnumTypeMetadata]]:
        """Parse all source files and extract declarations.

        Returns
        -------
            Tuple of (objects, enums) dictionaries.
        """
        # Phase 1: Parse all files
        self._parse_files()

        # Phase 2: Collect declaration names (for forward references)
        self._collect_declaration_names()

        # Phase 3: Build namespace and resolver
        self._build_namespace()

        # Phase 4: Extract full declarations
        self._extract_declarations()

        return self._objects, self._enums

    def _parse_files(self) -> None:
        """Parse all source files into ASTs."""
        for file_path in self.source_files:
            try:
                source = file_path.read_text(encoding="utf-8")
                tree = ast.parse(source, filename=str(file_path))
                self._asts[file_path] = tree
            except SyntaxError as e:  # noqa: PERF203
                msg = f"Syntax error in {file_path}: {e.msg}"
                location = LocationMetadata(
                    file=str(file_path),
                    line=e.lineno or 0,
                    column=e.offset or 0,
                )
                raise ParseError(msg, location=location) from e
            except OSError as e:
                msg = f"Failed to read {file_path}: {e}"
                raise ParseError(msg) from e

    def _collect_declaration_names(self) -> None:
        """Collect names of decorated classes for forward reference resolution."""
        for tree in self._asts.values():
            for node in ast.walk(tree):
                if isinstance(node, ast.ClassDef):
                    if has_decorator(node, "object_type"):
                        self._declared_objects.add(node.name)
                    elif has_decorator(node, "interface"):
                        self._declared_interfaces.add(node.name)
                    elif has_decorator(node, "enum_type") or self._is_enum_subclass(
                        node
                    ):
                        self._declared_enums.add(node.name)

    def _is_enum_subclass(self, node: ast.ClassDef) -> bool:
        """Check if a class inherits from enum.Enum."""
        for base in node.bases:
            if isinstance(base, ast.Attribute) and base.attr == "Enum":
                return True
            if isinstance(base, ast.Name) and base.id == "Enum":
                return True
        return False

    def _build_namespace(self) -> None:
        """Build the namespace for type resolution."""
        # Combine all ASTs for namespace building
        self._namespace = StubNamespace()

        for tree in self._asts.values():
            self._add_imports_from_tree(tree)

        # Add declared types
        all_declared = (
            self._declared_objects | self._declared_interfaces | self._declared_enums
        )
        for name in all_declared:
            self._namespace.add_declared_type(name)

        # Create resolver
        self._resolver = TypeResolver(
            namespace=self._namespace,
            declared_objects=self._declared_objects,
            declared_enums=self._declared_enums,
            declared_interfaces=self._declared_interfaces,
        )

    def _add_imports_from_tree(self, tree: ast.Module) -> None:
        """Extract imports from an AST tree and add to namespace."""
        assert self._namespace is not None
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                for alias in node.names:
                    self._namespace.add_import(alias.name, alias.asname)
            elif isinstance(node, ast.ImportFrom):
                module = node.module or ""
                for alias in node.names:
                    if alias.name != "*":
                        self._namespace.add_from_import(
                            module, alias.name, alias.asname
                        )

    def _extract_declarations(self) -> None:
        """Extract full declarations from all ASTs."""
        for file_path, tree in self._asts.items():
            for node in ast.iter_child_nodes(tree):
                if isinstance(node, ast.ClassDef):
                    self._extract_class(node, file_path)

    def _extract_class(self, node: ast.ClassDef, file_path: Path) -> None:
        """Extract a class declaration."""
        # Check for @object_type
        if has_decorator(node, "object_type"):
            obj = self._parse_object_type(node, file_path, is_interface=False)
            self._objects[obj.name] = obj
            return

        # Check for @interface
        if has_decorator(node, "interface"):
            obj = self._parse_object_type(node, file_path, is_interface=True)
            self._objects[obj.name] = obj
            return

        # Check for @enum_type or enum.Enum subclass
        if has_decorator(node, "enum_type") or self._is_enum_subclass(node):
            enum = self._parse_enum_type(node, file_path)
            self._enums[enum.name] = enum

    def _parse_object_type(
        self,
        node: ast.ClassDef,
        file_path: Path,
        is_interface: bool,
    ) -> ObjectTypeMetadata:
        """Parse an @object_type or @interface decorated class."""
        assert self._resolver is not None

        self._resolver.set_current_class(node.name)

        # Get decorator info for metadata
        decorator_type = "interface" if is_interface else "object_type"
        decorator = find_decorator(node, decorator_type)
        decorator_info = extract_decorator_info(decorator) if decorator else None

        # Extract deprecation
        deprecated = None
        if decorator_info and "deprecated" in decorator_info.kwargs:
            deprecated = decorator_info.kwargs["deprecated"]

        # Extract fields and functions
        fields: list[FieldMetadata] = []
        functions: list[FunctionMetadata] = []
        constructor: FunctionMetadata | None = None

        # Track field names for type hints resolution
        field_annotations: dict[str, ast.expr] = {}

        for item in node.body:
            # Extract annotated assignments (field declarations)
            if isinstance(item, ast.AnnAssign) and isinstance(item.target, ast.Name):
                field_annotations[item.target.id] = item.annotation
                field = self._parse_field(item, file_path, node.name)
                if field is not None:
                    fields.append(field)

            # Extract methods
            elif isinstance(item, (ast.FunctionDef, ast.AsyncFunctionDef)):
                # Check for @classmethod create() - alternative constructor
                if item.name == "create" and is_classmethod(item):
                    constructor = self._parse_constructor(item, file_path, node.name)
                elif has_decorator(item, "function"):
                    func = self._parse_function(item, file_path, node.name)
                    functions.append(func)

        # If no alternative constructor, create one from __init__
        if constructor is None and not is_interface:
            constructor = self._create_default_constructor(node, fields, file_path)

        self._resolver.set_current_class(None)

        return ObjectTypeMetadata(
            name=node.name,
            is_interface=is_interface,
            doc=get_docstring(node),
            deprecated=deprecated,
            fields=fields,
            functions=functions,
            constructor=constructor,
            location=get_location(node, str(file_path)),
        )

    def _parse_field(
        self,
        node: ast.AnnAssign,
        file_path: Path,
        class_name: str,
    ) -> FieldMetadata | None:
        """Parse a field declaration.

        Only returns a FieldMetadata if the field uses dagger.field().
        """
        assert self._resolver is not None
        assert isinstance(node.target, ast.Name)

        python_name = node.target.id

        # Check if this is a dagger.field() call
        has_field_decorator = False
        field_kwargs: dict[str, Any] = {}

        if node.value is not None and isinstance(node.value, ast.Call):
            func = node.value.func
            # Check for field(), dagger.field(), mod.field()
            is_name_field = isinstance(func, ast.Name) and func.id == "field"
            is_attr_field = isinstance(func, ast.Attribute) and func.attr == "field"
            is_field_call = is_name_field or is_attr_field

            if is_field_call:
                has_field_decorator = True
                # Extract field() arguments
                for keyword in node.value.keywords:
                    if keyword.arg is not None:
                        value = self._eval_constant(keyword.value)
                        field_kwargs[keyword.arg] = value

        if not has_field_decorator:
            return None

        # Resolve type
        location = get_location(node, str(file_path))
        resolved_type = self._resolver.resolve(node.annotation, location=location)

        # Extract Annotated metadata
        annotated_meta = extract_annotated_metadata(node.annotation)

        # Get API name
        api_name = field_kwargs.get("name") or normalize_name(python_name)

        # Get default value
        has_default = "default" in field_kwargs or "default_factory" in field_kwargs
        default_value = field_kwargs.get("default")

        return FieldMetadata(
            python_name=python_name,
            api_name=api_name,
            type_annotation=get_annotation_string(node.annotation),
            resolved_type=resolved_type,
            has_default=has_default,
            default_value=self._serialize_default(default_value),
            deprecated=field_kwargs.get("deprecated"),
            init=field_kwargs.get("init", True),
            doc=annotated_meta.doc,
            location=location,
        )

    def _parse_function(
        self,
        node: ast.FunctionDef | ast.AsyncFunctionDef,
        file_path: Path,
        class_name: str,
    ) -> FunctionMetadata:
        """Parse a @function decorated method."""
        assert self._resolver is not None

        # Get decorator info
        decorator = find_decorator(node, "function")
        decorator_info = extract_decorator_info(decorator) if decorator else None

        # Check for @check and @generate decorators
        is_check = has_decorator(node, "check")
        is_generate = has_decorator(node, "generate")

        # Extract decorator kwargs
        func_kwargs: dict[str, Any] = {}
        if decorator_info:
            func_kwargs = decorator_info.kwargs

        # Get API name
        python_name = node.name
        api_name = func_kwargs.get("name") or normalize_name(python_name)

        # Parse return type
        location = get_location(node, str(file_path))
        if node.returns:
            return_annotation = get_annotation_string(node.returns)
            resolved_return = self._resolver.resolve(node.returns, location=location)
        else:
            return_annotation = "None"
            resolved_return = ResolvedType(kind="void", name="None", is_optional=True)

        # Parse parameters
        parameters = self._parse_parameters(node, file_path)

        return FunctionMetadata(
            python_name=python_name,
            api_name=api_name,
            return_type_annotation=return_annotation,
            resolved_return_type=resolved_return,
            parameters=parameters,
            doc=func_kwargs.get("doc") or get_docstring(node),
            deprecated=func_kwargs.get("deprecated"),
            cache_policy=func_kwargs.get("cache"),
            is_check=is_check,
            is_generate=is_generate,
            is_async=isinstance(node, ast.AsyncFunctionDef),
            is_classmethod=is_classmethod(node),
            is_constructor=False,
            location=location,
        )

    def _parse_constructor(
        self,
        node: ast.FunctionDef | ast.AsyncFunctionDef,
        file_path: Path,
        class_name: str,
    ) -> FunctionMetadata:
        """Parse an alternative constructor (classmethod create)."""
        assert self._resolver is not None

        location = get_location(node, str(file_path))

        # Parse return type (should be Self or the class name)
        if node.returns:
            return_annotation = get_annotation_string(node.returns)
            resolved_return = self._resolver.resolve(node.returns, location=location)
        else:
            return_annotation = class_name
            resolved_return = ResolvedType(kind="object", name=class_name)

        # Parse parameters (skip cls for classmethod)
        parameters = self._parse_parameters(node, file_path, skip_first=True)

        return FunctionMetadata(
            python_name=node.name,
            api_name="",  # Constructor has empty API name
            return_type_annotation=return_annotation,
            resolved_return_type=resolved_return,
            parameters=parameters,
            doc=get_docstring(node),
            deprecated=None,
            cache_policy=None,
            is_check=False,
            is_async=isinstance(node, ast.AsyncFunctionDef),
            is_classmethod=True,
            is_constructor=True,
            location=location,
        )

    def _create_default_constructor(
        self,
        node: ast.ClassDef,
        fields: list[FieldMetadata],
        file_path: Path,
    ) -> FunctionMetadata:
        """Create a default constructor from dataclass-style fields."""
        assert self._resolver is not None

        # Convert fields to constructor parameters
        parameters: list[ParameterMetadata] = [
            ParameterMetadata(
                python_name=field.python_name,
                api_name=field.api_name,
                type_annotation=field.type_annotation,
                resolved_type=field.resolved_type,
                is_nullable=field.resolved_type.is_optional,
                has_default=field.has_default,
                default_value=field.default_value,
                doc=field.doc,
                deprecated=field.deprecated,
                location=field.location,
            )
            for field in fields
            if field.init
        ]

        return FunctionMetadata(
            python_name="__init__",
            api_name="",  # Constructor has empty API name
            return_type_annotation=node.name,
            resolved_return_type=ResolvedType(kind="object", name=node.name),
            parameters=parameters,
            doc=get_docstring(node),
            deprecated=None,
            cache_policy=None,
            is_check=False,
            is_async=False,
            is_classmethod=False,
            is_constructor=True,
            location=get_location(node, str(file_path)),
        )

    def _parse_parameters(
        self,
        node: ast.FunctionDef | ast.AsyncFunctionDef,
        file_path: Path,
        skip_first: bool = True,  # Skip 'self' or 'cls'
    ) -> list[ParameterMetadata]:
        """Parse function parameters."""
        assert self._resolver is not None

        parameters: list[ParameterMetadata] = []
        args = node.args

        # Combine all parameter types
        all_args = list(args.args)
        if args.kwonlyargs:
            all_args.extend(args.kwonlyargs)

        # Get defaults
        defaults_offset = len(all_args) - len(args.defaults)
        kw_defaults = {
            arg.arg: default
            for arg, default in zip(args.kwonlyargs, args.kw_defaults, strict=False)
            if default
        }

        for i, arg in enumerate(all_args):
            # Skip self/cls
            if skip_first and i == 0 and arg.arg in ("self", "cls"):
                continue

            python_name = arg.arg
            location = get_location(arg, str(file_path))

            # Get annotation
            if arg.annotation:
                annotation = arg.annotation
                annotation_str = get_annotation_string(annotation)

                # Extract Annotated metadata
                annotated_meta = extract_annotated_metadata(annotation)

                # Resolve type
                resolved_type = self._resolver.resolve(annotation, location=location)
            else:
                annotation_str = "Any"
                annotated_meta = None
                resolved_type = ResolvedType(kind="primitive", name="Any")

            # Get default value
            has_default = False
            default_value = None

            # Check positional defaults
            if i >= defaults_offset and i - defaults_offset < len(args.defaults):
                has_default = True
                default_node = args.defaults[i - defaults_offset]
                default_value = self._eval_constant(default_node)

            # Check keyword-only defaults
            if arg.arg in kw_defaults:
                has_default = True
                default_value = self._eval_constant(kw_defaults[arg.arg])

            # Get API name (from Name() annotation or normalized)
            api_name = normalize_name(python_name)
            if annotated_meta and annotated_meta.name:
                api_name = annotated_meta.name

            # Extract optional metadata values
            if annotated_meta:
                meta_doc = annotated_meta.doc
                meta_ignore = annotated_meta.ignore
                meta_default_path = annotated_meta.default_path
                meta_default_addr = annotated_meta.default_address
                meta_deprecated = annotated_meta.deprecated
                meta_alt_name = annotated_meta.name
            else:
                meta_doc = meta_ignore = meta_default_path = None
                meta_default_addr = meta_deprecated = meta_alt_name = None

            parameters.append(
                ParameterMetadata(
                    python_name=python_name,
                    api_name=api_name,
                    type_annotation=annotation_str,
                    resolved_type=resolved_type,
                    is_nullable=resolved_type.is_optional,
                    has_default=has_default,
                    default_value=self._serialize_default(default_value),
                    doc=meta_doc,
                    ignore=meta_ignore,
                    default_path=meta_default_path,
                    default_address=meta_default_addr,
                    deprecated=meta_deprecated,
                    alt_name=meta_alt_name,
                    location=location,
                )
            )

        return parameters

    def _parse_enum_type(
        self,
        node: ast.ClassDef,
        file_path: Path,
    ) -> EnumTypeMetadata:
        """Parse an enum type declaration."""
        members: list[EnumMemberMetadata] = []

        # Extract enum members
        for i, item in enumerate(node.body):
            if isinstance(item, ast.Assign):
                for target in item.targets:
                    if isinstance(target, ast.Name):
                        member_name = target.id
                        # Get value
                        if isinstance(item.value, ast.Constant):
                            value = str(item.value.value)
                        else:
                            value = member_name

                        # Check for docstring following assignment
                        doc = None
                        next_idx = i + 1
                        if next_idx < len(node.body):
                            next_item = node.body[next_idx]
                            if (
                                isinstance(next_item, ast.Expr)
                                and isinstance(next_item.value, ast.Constant)
                                and isinstance(next_item.value.value, str)
                            ):
                                doc = next_item.value.value.strip()

                        members.append(
                            EnumMemberMetadata(
                                name=member_name,
                                value=value,
                                doc=doc,
                            )
                        )

        return EnumTypeMetadata(
            name=node.name,
            doc=get_docstring(node),
            members=members,
            location=get_location(node, str(file_path)),
        )

    def _eval_constant(self, node: ast.expr | None) -> Any:  # noqa: PLR0911, C901
        """Evaluate a constant expression to a Python value."""
        if node is None:
            return None

        if isinstance(node, ast.Constant):
            return node.value
        if isinstance(node, ast.Name):
            if node.id == "True":
                return True
            if node.id == "False":
                return False
            if node.id == "None":
                return None
            return node.id  # Return as string
        if isinstance(node, ast.List):
            return [self._eval_constant(el) for el in node.elts]
        if isinstance(node, ast.Tuple):
            return tuple(self._eval_constant(el) for el in node.elts)
        if isinstance(node, ast.Dict):
            return {
                self._eval_constant(k) if k else None: self._eval_constant(v)
                for k, v in zip(node.keys, node.values, strict=False)
            }
        if isinstance(node, ast.UnaryOp) and isinstance(node.op, ast.USub):
            val = self._eval_constant(node.operand)
            if isinstance(val, (int, float)):
                return -val

        # Can't evaluate - return None (will be handled at runtime)
        return None

    def _serialize_default(self, value: Any) -> Any:
        """Serialize a default value for JSON storage.

        Complex values that can't be serialized are returned as None.
        """
        if value is None:
            return None

        # Try to serialize to JSON
        try:
            json.dumps(value)
        except (TypeError, ValueError):
            return None
        else:
            return value
