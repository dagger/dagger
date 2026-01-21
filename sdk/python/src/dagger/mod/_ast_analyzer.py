"""AST-based module analyzer for extracting Dagger module definitions."""

from __future__ import annotations

import ast
import inspect
import logging
import re
from pathlib import Path
from typing import Any

from graphql.pyutils import snake_to_camel

from dagger.mod._ast_types import (
    ASTEnumDef,
    ASTEnumMember,
    ASTFieldDef,
    ASTFunctionDef,
    ASTModuleInfo,
    ASTObjectDef,
    ASTParameter,
    ASTTypeAnnotation,
)

logger = logging.getLogger(__name__)

# Known Dagger decorator names
OBJECT_DECORATORS = frozenset({"object_type"})
FUNCTION_DECORATORS = frozenset({"function"})
ENUM_DECORATORS = frozenset({"enum_type"})
INTERFACE_DECORATORS = frozenset({"interface"})
CHECK_DECORATORS = frozenset({"check"})
FIELD_FUNCTIONS = frozenset({"field"})

# Known Dagger API types (from codegen)
DAGGER_API_TYPES = frozenset({
    "Container", "Directory", "File", "Secret", "Socket",
    "Service", "CacheVolume", "Terminal", "ModuleSource",
    "GitRepository", "GitRef", "JSON", "Module", "Function",
    "TypeDef", "FieldTypeDef", "FunctionArg", "EnumTypeDef",
    "ObjectTypeDef", "InterfaceTypeDef", "ListTypeDef",
    "ScalarTypeDef", "InputTypeDef", "SourceMap",
    "GeneratedCode", "Host", "Port", "Label", "EnvVariable",
    "CurrentModule", "FunctionCall", "Engine", "EngineCache",
    "EngineCacheEntry", "EngineCacheEntrySet", "SDKConfig",
    "LLM", "Error", "ErrorValue", "Changeset", "Check", "CheckGroup",
    # Scalar types
    "ContainerID", "DirectoryID", "FileID", "SecretID", "SocketID",
    "ServiceID", "CacheVolumeID", "TerminalID", "ModuleSourceID",
    "GitRepositoryID", "GitRefID", "ModuleID", "FunctionID",
    "TypeDefID", "GeneratedCodeID", "LLMID", "Platform", "Void",
})


def to_camel_case(s: str) -> str:
    """Convert a string to camelCase."""
    return snake_to_camel(s.replace("-", "_"), upper=False)


def normalize_name(name: str) -> str:
    """Remove trailing underscore used to avoid conflicts with reserved words."""
    if name.endswith("_") and len(name) > 1 and name[-2] != "_" and not name.startswith("_"):
        return name.removesuffix("_")
    return name


class ModuleAnalyzer:
    """Analyzes Python source files without loading them."""

    def __init__(self):
        # Track imports for name resolution
        self._imports: dict[str, str] = {}  # alias -> module
        self._from_imports: dict[str, tuple[str, str]] = {}  # alias -> (module, name)
        self._star_imports: set[str] = set()  # modules with star imports

    def analyze_file(self, file_path: Path) -> ASTModuleInfo:
        """Parse and analyze a Python source file."""
        source = file_path.read_text(encoding="utf-8")
        tree = ast.parse(source, filename=str(file_path))
        return self._analyze_module(tree, file_path, source)

    def analyze_source(self, source: str, filename: str = "<string>") -> ASTModuleInfo:
        """Parse and analyze Python source code."""
        tree = ast.parse(source, filename=filename)
        return self._analyze_module(tree, Path(filename), source)

    def _analyze_module(
        self,
        tree: ast.Module,
        file_path: Path,
        source: str,
    ) -> ASTModuleInfo:
        """Extract all relevant definitions from a module AST."""
        # Reset state
        self._imports = {}
        self._from_imports = {}
        self._star_imports = set()

        # First pass: collect imports
        self._collect_imports(tree)

        # Get module docstring
        module_doc = ast.get_docstring(tree)

        # Second pass: find decorated classes
        objects: list[ASTObjectDef] = []
        enums: list[ASTEnumDef] = []

        for node in tree.body:
            if isinstance(node, ast.ClassDef):
                if self._has_decorator(node, OBJECT_DECORATORS):
                    obj = self._analyze_object_class(node, source)
                    objects.append(obj)
                elif self._has_decorator(node, INTERFACE_DECORATORS):
                    obj = self._analyze_object_class(node, source, is_interface=True)
                    objects.append(obj)
                elif self._has_decorator(node, ENUM_DECORATORS):
                    enum = self._analyze_enum_class(node, source)
                    enums.append(enum)

        return ASTModuleInfo(
            objects=objects,
            enums=enums,
            source_path=file_path,
            module_doc=module_doc,
        )

    def _collect_imports(self, tree: ast.Module):
        """Collect all import statements for name resolution."""
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                for alias in node.names:
                    name = alias.asname or alias.name
                    self._imports[name] = alias.name
            elif isinstance(node, ast.ImportFrom):
                module = node.module or ""
                for alias in node.names:
                    if alias.name == "*":
                        self._star_imports.add(module)
                    else:
                        name = alias.asname or alias.name
                        self._from_imports[name] = (module, alias.name)

    def _has_decorator(
        self,
        node: ast.ClassDef | ast.FunctionDef | ast.AsyncFunctionDef,
        names: frozenset[str],
    ) -> bool:
        """Check if node has any of the specified decorators."""
        for decorator in node.decorator_list:
            dec_name = self._get_decorator_base_name(decorator)
            if dec_name in names:
                return True
        return False

    def _get_decorator_base_name(self, decorator: ast.expr) -> str:
        """Extract the base decorator name (without module prefix)."""
        if isinstance(decorator, ast.Name):
            # @object_type
            return decorator.id
        elif isinstance(decorator, ast.Attribute):
            # @dagger.object_type or @mod.function
            return decorator.attr
        elif isinstance(decorator, ast.Call):
            # @object_type(...) or @function(name="...")
            return self._get_decorator_base_name(decorator.func)
        return ""

    def _get_decorator_kwargs(
        self,
        node: ast.ClassDef | ast.FunctionDef | ast.AsyncFunctionDef,
        names: frozenset[str],
    ) -> dict[str, Any]:
        """Extract keyword arguments from a decorator call."""
        for decorator in node.decorator_list:
            dec_name = self._get_decorator_base_name(decorator)
            if dec_name in names and isinstance(decorator, ast.Call):
                kwargs = {}
                for kw in decorator.keywords:
                    if kw.arg is not None:
                        val, _ = self._extract_literal_value(kw.value)
                        kwargs[kw.arg] = val
                return kwargs
        return {}

    def _analyze_object_class(
        self,
        node: ast.ClassDef,
        source: str,
        is_interface: bool = False,
    ) -> ASTObjectDef:
        """Analyze a class decorated with @object_type or @interface."""
        docstring = ast.get_docstring(node)

        # Get decorator kwargs (e.g., deprecated)
        dec_kwargs = self._get_decorator_kwargs(node, OBJECT_DECORATORS | INTERFACE_DECORATORS)
        deprecated = dec_kwargs.get("deprecated")

        fields: list[ASTFieldDef] = []
        functions: list[ASTFunctionDef] = []
        constructor: ASTFunctionDef | None = None

        for item in node.body:
            # Analyze annotated assignments (fields)
            if isinstance(item, ast.AnnAssign) and isinstance(item.target, ast.Name):
                field = self._analyze_field(item, source)
                if field is not None:
                    fields.append(field)

            # Analyze methods
            elif isinstance(item, (ast.FunctionDef, ast.AsyncFunctionDef)):
                if self._has_decorator(item, FUNCTION_DECORATORS):
                    func = self._analyze_function(item, node.name, source)
                    functions.append(func)
                elif item.name == "__init__" and self._has_decorator(item, FUNCTION_DECORATORS):
                    # Constructor explicitly decorated
                    func = self._analyze_function(item, node.name, source)
                    func.is_constructor = True
                    constructor = func

        return ASTObjectDef(
            name=node.name,
            docstring=docstring,
            fields=fields,
            functions=functions,
            constructor=constructor,
            deprecated=deprecated,
            is_interface=is_interface,
            line_number=node.lineno,
        )

    def _analyze_field(
        self,
        node: ast.AnnAssign,
        source: str,
    ) -> ASTFieldDef | None:
        """Analyze a field annotation, only if it uses field()."""
        if not isinstance(node.target, ast.Name):
            return None

        field_name = node.target.id

        # Check if this is a dagger field (uses field() function)
        if node.value is None:
            return None

        # Check if value is a call to field()
        field_kwargs = self._extract_field_call_kwargs(node.value)
        if field_kwargs is None:
            return None

        # Parse the annotation
        annotation = self._parse_annotation(node.annotation)

        # API name
        alt_name = field_kwargs.get("name")
        api_name = alt_name if alt_name else to_camel_case(normalize_name(field_name))

        return ASTFieldDef(
            name=api_name,
            original_name=field_name,
            annotation=annotation,
            has_default="default" in field_kwargs or "default_factory" in field_kwargs,
            default_value=field_kwargs.get("default"),
            deprecated=field_kwargs.get("deprecated"),
            line_number=node.lineno,
        )

    def _extract_field_call_kwargs(self, node: ast.expr) -> dict[str, Any] | None:
        """Extract kwargs from a field() call, or None if not a field call."""
        if not isinstance(node, ast.Call):
            return None

        # Check if this is a call to field()
        func_name = self._get_call_func_name(node.func)
        if func_name not in FIELD_FUNCTIONS:
            return None

        kwargs = {}
        for kw in node.keywords:
            if kw.arg is not None:
                val, _ = self._extract_literal_value(kw.value)
                kwargs[kw.arg] = val
        return kwargs

    def _get_call_func_name(self, node: ast.expr) -> str:
        """Get the function name from a call expression."""
        if isinstance(node, ast.Name):
            return node.id
        elif isinstance(node, ast.Attribute):
            return node.attr
        return ""

    def _analyze_function(
        self,
        node: ast.FunctionDef | ast.AsyncFunctionDef,
        class_name: str,
        source: str,
    ) -> ASTFunctionDef:
        """Analyze a function decorated with @function."""
        docstring = ast.get_docstring(node)

        # Check for @check decorator
        is_check = self._has_decorator(node, CHECK_DECORATORS)

        # Get @function decorator kwargs
        dec_kwargs = self._get_decorator_kwargs(node, FUNCTION_DECORATORS)

        # Parse parameters (skip 'self')
        parameters: list[ASTParameter] = []
        for arg in node.args.args:
            if arg.arg == "self":
                continue
            param = self._analyze_parameter(arg, node.args.defaults, node.args.args)
            parameters.append(param)

        # Parse return annotation
        return_annotation = self._parse_annotation(node.returns)

        # Handle Self return type
        if return_annotation and return_annotation.base_type == "Self":
            return_annotation.base_type = class_name

        # API name
        alt_name = dec_kwargs.get("name")
        api_name = alt_name if alt_name else to_camel_case(normalize_name(node.name))

        return ASTFunctionDef(
            name=api_name,
            original_name=node.name,
            parameters=parameters,
            return_annotation=return_annotation,
            docstring=docstring,
            is_async=isinstance(node, ast.AsyncFunctionDef),
            alt_name=alt_name,
            alt_doc=dec_kwargs.get("doc"),
            cache_policy=dec_kwargs.get("cache"),
            deprecated=dec_kwargs.get("deprecated"),
            is_check=is_check,
            is_constructor=False,
            line_number=node.lineno,
        )

    def _analyze_parameter(
        self,
        arg: ast.arg,
        defaults: list[ast.expr],
        all_args: list[ast.arg],
    ) -> ASTParameter:
        """Analyze a function parameter."""
        # Parse annotation
        annotation = self._parse_annotation(arg.annotation)

        # Extract metadata from Annotated if present
        alt_name = None
        doc = None
        default_path = None
        ignore = None
        deprecated = None

        if annotation:
            for kind, value in annotation.annotated_metadata:
                if kind == "name":
                    alt_name = value
                elif kind == "doc":
                    doc = value
                elif kind == "default_path":
                    default_path = value
                elif kind == "ignore":
                    ignore = value
                elif kind == "deprecated":
                    deprecated = value

        # Check for default value
        has_default = False
        default_value = None

        # Calculate which defaults correspond to which args
        # defaults are right-aligned with args
        num_args = len(all_args)
        num_defaults = len(defaults)
        arg_index = all_args.index(arg)

        # Skip 'self' in calculation
        non_self_index = arg_index - 1 if all_args[0].arg == "self" else arg_index
        non_self_count = num_args - 1 if all_args[0].arg == "self" else num_args

        default_start_index = non_self_count - num_defaults
        if non_self_index >= default_start_index:
            default_idx = non_self_index - default_start_index
            if 0 <= default_idx < len(defaults):
                has_default = True
                default_value, _ = self._extract_literal_value(defaults[default_idx])

        # API name
        name = alt_name if alt_name else to_camel_case(normalize_name(arg.arg))

        return ASTParameter(
            name=name,
            annotation=annotation,
            default_value=default_value,
            has_default=has_default,
            alt_name=alt_name,
            doc=doc,
            default_path=default_path,
            ignore=ignore,
            deprecated=deprecated,
        )

    def _analyze_enum_class(self, node: ast.ClassDef, source: str) -> ASTEnumDef:
        """Analyze an enum class decorated with @enum_type."""
        docstring = ast.get_docstring(node)
        members: list[ASTEnumMember] = []

        body = node.body
        for i, item in enumerate(body):
            if isinstance(item, ast.Assign):
                for target in item.targets:
                    if isinstance(target, ast.Name):
                        member_name = target.id

                        # Extract value
                        value, _ = self._extract_literal_value(item.value)
                        if value is None:
                            value = member_name

                        # Look for docstring after the assignment
                        member_doc = self._extract_doc_after_stmt(body, i)

                        # Parse docstring for deprecated directive
                        deprecated = None
                        if member_doc:
                            member_doc, deprecated = self._parse_enum_member_doc(member_doc)

                        members.append(ASTEnumMember(
                            name=member_name,
                            value=str(value),
                            docstring=member_doc,
                            deprecated=deprecated,
                            line_number=item.lineno,
                        ))

        return ASTEnumDef(
            name=node.name,
            docstring=docstring,
            members=members,
            line_number=node.lineno,
        )

    def _extract_doc_after_stmt(self, body: list[ast.stmt], index: int) -> str | None:
        """Extract docstring from the statement following the given index."""
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

    def _parse_enum_member_doc(self, text: str) -> tuple[str | None, str | None]:
        """Parse enum member docstring, extracting deprecated directive."""
        description_lines: list[str] = []
        deprecated_lines: list[str] = []
        lines = text.splitlines()
        it = iter(enumerate(lines))

        for _, raw_line in it:
            stripped = raw_line.strip()
            if stripped.startswith(".. deprecated::"):
                remainder = stripped[len(".. deprecated::"):].strip()
                if remainder:
                    deprecated_lines.append(remainder)
                # Get continuation lines
                for _, cont in it:
                    cont_stripped = cont.strip()
                    if not cont_stripped:
                        continue
                    if cont.startswith(("   ", "\t")):
                        deprecated_lines.append(cont_stripped)
                        continue
                    description_lines.append(cont_stripped)
                    break
            else:
                description_lines.append(stripped)

        description = "\n".join(line for line in description_lines if line).strip() or None
        deprecated = "\n".join(line for line in deprecated_lines if line).strip() or None
        return description, deprecated

    def _parse_annotation(self, node: ast.expr | None) -> ASTTypeAnnotation | None:
        """Parse a type annotation from an AST node."""
        if node is None:
            return None

        raw = ast.unparse(node)

        # String literal (forward reference)
        if isinstance(node, ast.Constant) and isinstance(node.value, str):
            return ASTTypeAnnotation(
                raw=raw,
                base_type=node.value,
            )

        # Simple name: str, int, Container
        if isinstance(node, ast.Name):
            return ASTTypeAnnotation(
                raw=raw,
                base_type=node.id,
            )

        # Attribute access: dagger.Container
        if isinstance(node, ast.Attribute):
            return ASTTypeAnnotation(
                raw=raw,
                base_type=node.attr,
            )

        # Subscript: list[str], Optional[str], Annotated[str, Doc("...")]
        if isinstance(node, ast.Subscript):
            return self._parse_subscript_annotation(node, raw)

        # Binary or: str | None
        if isinstance(node, ast.BinOp) and isinstance(node.op, ast.BitOr):
            return self._parse_union_annotation(node, raw)

        # Fallback
        return ASTTypeAnnotation(raw=raw, base_type=raw)

    def _parse_subscript_annotation(
        self,
        node: ast.Subscript,
        raw: str,
    ) -> ASTTypeAnnotation:
        """Parse a subscripted type annotation."""
        base = self._parse_annotation(node.value)
        base_type = base.base_type if base else "Unknown"

        # Get type arguments
        if isinstance(node.slice, ast.Tuple):
            args = [self._parse_annotation(el) for el in node.slice.elts]
        else:
            args = [self._parse_annotation(node.slice)]

        # Filter None args
        args = [a for a in args if a is not None]

        # Handle Annotated specially
        if base_type in ("Annotated", "typing.Annotated", "typing_extensions.Annotated"):
            if args:
                actual_type = args[0]
                metadata = self._extract_annotated_metadata(node.slice)
                actual_type.annotated_metadata = metadata
                actual_type.raw = raw
                return actual_type
            return ASTTypeAnnotation(raw=raw, base_type="Unknown")

        # Handle Optional
        if base_type in ("Optional", "typing.Optional"):
            if args:
                result = args[0]
                result.is_optional = True
                result.raw = raw
                return result
            return ASTTypeAnnotation(raw=raw, base_type="Unknown", is_optional=True)

        # Handle Union
        if base_type in ("Union", "typing.Union"):
            return self._parse_union_args(args, raw)

        # Regular generic type
        return ASTTypeAnnotation(
            raw=raw,
            base_type=base_type,
            type_args=args,
        )

    def _parse_union_annotation(self, node: ast.BinOp, raw: str) -> ASTTypeAnnotation:
        """Parse a union type annotation (X | Y)."""
        left = self._parse_annotation(node.left)
        right = self._parse_annotation(node.right)

        args = []
        if left:
            args.append(left)
        if right:
            args.append(right)

        return self._parse_union_args(args, raw)

    def _parse_union_args(
        self,
        args: list[ASTTypeAnnotation],
        raw: str,
    ) -> ASTTypeAnnotation:
        """Parse union type arguments, detecting Optional pattern."""
        # Check for None in union (Optional pattern)
        non_none_args = [a for a in args if a.base_type not in ("None", "NoneType")]
        has_none = len(non_none_args) < len(args)

        if len(non_none_args) == 1:
            result = non_none_args[0]
            result.is_optional = has_none
            result.raw = raw
            return result

        # Multiple non-None types - not fully supported, take first
        if non_none_args:
            result = non_none_args[0]
            result.is_optional = has_none
            result.raw = raw
            return result

        # Just None
        return ASTTypeAnnotation(raw=raw, base_type="None")

    def _extract_annotated_metadata(self, slice_node: ast.expr) -> list[tuple[str, Any]]:
        """Extract metadata from Annotated[T, meta1, meta2, ...]."""
        metadata: list[tuple[str, Any]] = []

        if isinstance(slice_node, ast.Tuple) and len(slice_node.elts) > 1:
            # Skip first element (the actual type)
            for meta_node in slice_node.elts[1:]:
                if isinstance(meta_node, ast.Call):
                    meta = self._extract_metadata_call(meta_node)
                    if meta:
                        metadata.append(meta)

        return metadata

    def _extract_metadata_call(self, node: ast.Call) -> tuple[str, Any] | None:
        """Extract metadata from a call like Doc("...") or Name("...")."""
        func_name = self._get_call_func_name(node.func)

        if func_name == "Doc":
            if node.args:
                val, _ = self._extract_literal_value(node.args[0])
                return ("doc", val)
            for kw in node.keywords:
                if kw.arg == "documentation":
                    val, _ = self._extract_literal_value(kw.value)
                    return ("doc", val)

        elif func_name == "Name":
            if node.args:
                val, _ = self._extract_literal_value(node.args[0])
                return ("name", val)
            for kw in node.keywords:
                if kw.arg == "name":
                    val, _ = self._extract_literal_value(kw.value)
                    return ("name", val)

        elif func_name == "DefaultPath":
            if node.args:
                val, _ = self._extract_literal_value(node.args[0])
                return ("default_path", val)
            for kw in node.keywords:
                if kw.arg == "from_context":
                    val, _ = self._extract_literal_value(kw.value)
                    return ("default_path", val)

        elif func_name == "Ignore":
            if node.args:
                val, _ = self._extract_literal_value(node.args[0])
                return ("ignore", val)
            for kw in node.keywords:
                if kw.arg == "patterns":
                    val, _ = self._extract_literal_value(kw.value)
                    return ("ignore", val)

        elif func_name == "Deprecated":
            if node.args:
                val, _ = self._extract_literal_value(node.args[0])
                return ("deprecated", val or "")
            for kw in node.keywords:
                if kw.arg == "reason":
                    val, _ = self._extract_literal_value(kw.value)
                    return ("deprecated", val or "")
            # Deprecated() with no args means empty string
            return ("deprecated", "")

        return None

    def _extract_literal_value(self, node: ast.expr) -> tuple[Any, bool]:
        """
        Extract a literal value from an AST node.
        Returns (value, is_serializable).
        """
        if isinstance(node, ast.Constant):
            return node.value, True

        if isinstance(node, ast.List):
            items = []
            for el in node.elts:
                val, ok = self._extract_literal_value(el)
                if not ok:
                    return None, False
                items.append(val)
            return items, True

        if isinstance(node, ast.Tuple):
            items = []
            for el in node.elts:
                val, ok = self._extract_literal_value(el)
                if not ok:
                    return None, False
                items.append(val)
            return tuple(items), True

        if isinstance(node, ast.Dict):
            result = {}
            for k, v in zip(node.keys, node.values):
                if k is None:
                    return None, False
                key_val, ok = self._extract_literal_value(k)
                if not ok:
                    return None, False
                val_val, ok = self._extract_literal_value(v)
                if not ok:
                    return None, False
                result[key_val] = val_val
            return result, True

        if isinstance(node, ast.Set):
            items = set()
            for el in node.elts:
                val, ok = self._extract_literal_value(el)
                if not ok:
                    return None, False
                items.add(val)
            return items, True

        if isinstance(node, ast.UnaryOp) and isinstance(node.op, (ast.USub, ast.UAdd)):
            val, ok = self._extract_literal_value(node.operand)
            if ok and isinstance(val, (int, float)):
                return -val if isinstance(node.op, ast.USub) else val, True

        if isinstance(node, ast.Name):
            # Handle common constants
            if node.id == "None":
                return None, True
            if node.id == "True":
                return True, True
            if node.id == "False":
                return False, True

        # Non-literal value (function call, variable reference, etc.)
        return None, False

    def is_dagger_type(self, type_name: str) -> bool:
        """Check if a type name is a known Dagger API type."""
        return type_name in DAGGER_API_TYPES

    def resolve_type_name(self, name: str) -> str:
        """Resolve a type name through imports."""
        # Check if it's directly imported
        if name in self._from_imports:
            module, original_name = self._from_imports[name]
            return original_name

        # Check if it's a module alias
        if name in self._imports:
            return name

        # Check star imports for dagger types
        if "dagger" in self._star_imports and name in DAGGER_API_TYPES:
            return name

        return name
