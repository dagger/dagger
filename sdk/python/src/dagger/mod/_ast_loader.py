"""AST-based module loader for type registration without executing code."""

import ast
import contextlib
import dataclasses
import enum
import importlib.metadata
import importlib.util
import inspect
import logging
import os
import sys
import typing
from pathlib import Path
from typing import Any

from dagger.mod._exceptions import ModuleLoadError
from dagger.mod._module import CHECK_DEF_KEY, Module

logger = logging.getLogger(__package__)

MAIN_OBJECT: typing.Final[str] = os.getenv("DAGGER_MAIN_OBJECT", "")
ENTRY_POINT_NAME: typing.Final[str] = "main_object"
ENTRY_POINT_GROUP: typing.Final[str] = "dagger.mod"
IMPORT_PKG: typing.Final[str] = os.getenv("DAGGER_DEFAULT_PYTHON_PACKAGE", "main")


@dataclasses.dataclass
class ClassInfo:
    """Information about a class extracted from AST."""

    name: str
    bases: list[str]
    docstring: str | None
    decorator_type: str | None  # 'object_type', 'interface', 'enum_type'
    deprecated: str | None
    fields: dict[str, "FieldInfo"]
    methods: dict[str, "MethodInfo"]
    is_enum: bool = False
    enum_members: dict[str, tuple[Any, str | None, str | None]] = dataclasses.field(
        default_factory=dict
    )  # (value, description, deprecated)
    init_params: set[str] | None = None  # Parameters in __init__ if present
    init_method: "MethodInfo | None" = None  # Full __init__ method info if present


@dataclasses.dataclass
class FieldInfo:
    """Information about a field extracted from AST."""

    name: str
    type_annotation: str | None
    has_default: bool
    default_value: Any  # The actual default value, can be None
    is_dagger_field: bool
    deprecated: str | None
    field_name: str | None  # Alternative name from mod.field(name=...)
    init: bool = True  # Whether field should be in constructor (from mod.field(init=...))
    init_explicit: bool = False  # Whether init was explicitly set in field()
    default_explicit: bool = False  # Whether default was explicitly set in field()


@dataclasses.dataclass
class MethodInfo:
    """Information about a method extracted from AST."""

    name: str
    docstring: str | None
    is_function: bool  # Whether it has @function decorator
    is_async: bool
    is_classmethod: bool  # Whether it has @classmethod decorator
    parameters: list["ParamInfo"]
    return_type: str | None
    deprecated: str | None
    function_name: str | None  # Alternative name from mod.function(name=...)
    cache: str | None
    check: bool = False  # Whether it has @check decorator


@dataclasses.dataclass
class ParamInfo:
    """Information about a parameter extracted from AST."""

    name: str
    type_annotation: str | None
    has_default: bool
    default_value: Any  # Can be any Python value including None
    is_keyword_only: bool = False  # Whether this is a keyword-only parameter (after *)
    # Annotated metadata
    doc: str | None = None  # From Doc()
    api_name: str | None = None  # From Name()
    default_path: str | None = None  # From DefaultPath()
    ignore: list[str] | None = None  # From Ignore()
    deprecated: str | None = None  # From Deprecated()


def get_entry_point() -> importlib.metadata.EntryPoint:
    """Get the entry point for the main object."""
    sel = importlib.metadata.entry_points(
        group=ENTRY_POINT_GROUP,
        name=ENTRY_POINT_NAME,
    )
    if ep := next(iter(sel), None):
        return ep

    import_pkg = IMPORT_PKG

    # Fallback for modules that still use the "main" package name.
    if not importlib.util.find_spec(import_pkg):
        import_pkg = "main"

        if not importlib.util.find_spec(import_pkg):
            msg = (
                "Main object not found. You can configure it explicitly by adding "
                "an entry point to your pyproject.toml file. For example:\n"
                "\n"
                f'[project.entry-points."{ENTRY_POINT_GROUP}"]\n'
                f"{ENTRY_POINT_NAME} = '{IMPORT_PKG}:{MAIN_OBJECT}'\n"
            )
            raise ModuleLoadError(msg)

    return importlib.metadata.EntryPoint(
        group=ENTRY_POINT_GROUP,
        name=ENTRY_POINT_NAME,
        value=f"{import_pkg}:{MAIN_OBJECT}",
    )


def find_module_files(module_path: Path | None = None) -> list[Path]:
    """Find all Python files in the module.

    Args:
        module_path: Optional path to the module file or directory.
                     If None, will try to find from entry point or environment.
    """
    if module_path is None:
        # Try entry point first
        try:
            ep = get_entry_point()
            module_name = ep.value.split(":")[0]
        except ModuleLoadError:
            # Fall back to IMPORT_PKG environment variable
            module_name = IMPORT_PKG
            logger.debug("No entry point found, using IMPORT_PKG: %s", module_name)

        # Try to find the module's location
        spec = importlib.util.find_spec(module_name)
        if spec is None or spec.origin is None:
            # Last resort: check if there's a 'src' directory with the module
            cwd = Path.cwd()
            src_dir = cwd / "src" / module_name
            if src_dir.exists() and src_dir.is_dir():
                logger.debug("Found module in src directory: %s", src_dir)
                module_path = src_dir
            else:
                msg = (
                    f"Could not find module '{module_name}' "
                    f"(checked sys.path and {src_dir})"
                )
                raise ModuleLoadError(msg)
        else:
            module_path = Path(spec.origin)

    # If it's a directory, look for __init__.py
    if module_path.is_dir():
        module_dir = module_path
        return list(module_dir.glob("*.py"))
    # If it's __init__.py, get the directory
    if module_path.name == "__init__.py":
        module_dir = module_path.parent
        return list(module_dir.glob("*.py"))
    return [module_path]


def parse_decorator_call(decorator: ast.expr) -> tuple[str | None, dict[str, Any]]:
    """Parse a decorator to extract its name and arguments.

    Returns
    -------
        Tuple of (decorator_name, kwargs)
    """
    if isinstance(decorator, ast.Name):
        # Handle direct decorator like @object_type or @function
        return decorator.id, {}
    if isinstance(decorator, ast.Attribute):
        # Handle mod.object_type, mod.function, etc. (less common pattern)
        return decorator.attr, {}
    if isinstance(decorator, ast.Call):
        # Handle decorator with arguments like @function(name="foo")
        # or @object_type(deprecated="...")
        if isinstance(decorator.func, ast.Name):
            name = decorator.func.id
        elif isinstance(decorator.func, ast.Attribute):
            name = decorator.func.attr
        else:
            return None, {}

        kwargs = {}
        for keyword in decorator.keywords:
            if keyword.arg:
                # Try to evaluate simple literal values
                try:
                    kwargs[keyword.arg] = ast.literal_eval(keyword.value)
                except (ValueError, TypeError):
                    # If not a literal, convert to string representation
                    kwargs[keyword.arg] = ast.unparse(keyword.value)

        return name, kwargs

    return None, {}


def get_type_annotation(node: ast.expr | None) -> str | None:
    """Extract type annotation as a string."""
    if node is None:
        return None
    try:
        return ast.unparse(node)
    except Exception:  # noqa: BLE001
        return None


def parse_annotated_metadata(
    annotation_node: ast.expr | None,
) -> dict[str, Any]:
    """Extract metadata from Annotated type hints.

    Parses Doc(), Name(), DefaultPath(), Ignore(), and Deprecated() from Annotated.

    Returns
    -------
        Dict with keys: doc, api_name, default_path, ignore, deprecated
    """
    metadata = {
        "doc": None,
        "api_name": None,
        "default_path": None,
        "ignore": None,
        "deprecated": None,
    }

    if annotation_node is None:
        return metadata

    # Check if this is an Annotated type: Annotated[Type, metadata...]
    if not isinstance(annotation_node, ast.Subscript):
        return metadata

    # Check if it's Annotated (could be typing.Annotated or just Annotated)
    is_annotated = False
    if isinstance(annotation_node.value, ast.Name):
        is_annotated = annotation_node.value.id == "Annotated"
    elif isinstance(annotation_node.value, ast.Attribute):
        is_annotated = annotation_node.value.attr == "Annotated"

    if not is_annotated:
        return metadata

    # Extract the slice which contains [Type, metadata...]
    # In Python 3.9+, slice can be a Tuple or single element
    if isinstance(annotation_node.slice, ast.Tuple):
        # Annotated[Type, metadata1, metadata2, ...]
        # Skip first element (the actual type), parse the rest
        metadata_nodes = annotation_node.slice.elts[1:]
    else:
        # Single argument after type (shouldn't happen with Annotated, but handle it)
        return metadata

    # Parse each metadata node
    for meta_node in metadata_nodes:
        if not isinstance(meta_node, ast.Call):
            continue

        # Get the name of the metadata function
        meta_name = None
        if isinstance(meta_node.func, ast.Name):
            meta_name = meta_node.func.id
        elif isinstance(meta_node.func, ast.Attribute):
            meta_name = meta_node.func.attr

        if meta_name is None:
            continue

        # Extract the value based on metadata type
        try:
            if meta_name == "Doc" and meta_node.args:
                # Doc("description")
                metadata["doc"] = ast.literal_eval(meta_node.args[0])
            elif meta_name == "Name" and meta_node.args:
                # Name("api_name")
                metadata["api_name"] = ast.literal_eval(meta_node.args[0])
            elif meta_name == "DefaultPath" and meta_node.args:
                # DefaultPath("path")
                metadata["default_path"] = ast.literal_eval(meta_node.args[0])
            elif meta_name == "Ignore" and meta_node.args:
                # Ignore(["pattern1", "pattern2"])
                metadata["ignore"] = ast.literal_eval(meta_node.args[0])
            elif meta_name == "Deprecated":
                # Deprecated() or Deprecated("reason")
                if meta_node.args:
                    metadata["deprecated"] = ast.literal_eval(meta_node.args[0])
                else:
                    metadata["deprecated"] = ""  # Empty string means deprecated without reason
        except (ValueError, TypeError) as e:
            # If literal_eval fails, try using unparsed value for string-like metadata
            try:
                unparsed = ast.unparse(meta_node.args[0]) if meta_node.args else None
                if meta_name == "Doc" and unparsed:
                    metadata["doc"] = unparsed
                    logger.warning("Doc value is not a literal, using unparsed: %s", unparsed)
                elif meta_name == "Name" and unparsed:
                    metadata["api_name"] = unparsed
                    logger.warning("Name value is not a literal, using unparsed: %s", unparsed)
                elif meta_name == "DefaultPath" and unparsed:
                    metadata["default_path"] = unparsed
                    logger.warning("DefaultPath value is not a literal, using unparsed: %s", unparsed)
                elif meta_name == "Deprecated" and unparsed:
                    metadata["deprecated"] = unparsed
                    logger.warning("Deprecated value is not a literal, using unparsed: %s", unparsed)
                # For Ignore, we need a list, so skip if not a literal
                elif meta_name == "Ignore":
                    logger.warning("Ignore value is not a literal list, skipping")
            except Exception:  # noqa: BLE001
                # If even unparsing fails, log and skip
                logger.warning("Failed to extract %s metadata", meta_name)

    return metadata


def extract_docstring(node: ast.FunctionDef | ast.ClassDef | ast.Module) -> str | None:
    """Extract docstring from a node."""
    if (
        node.body
        and isinstance(node.body[0], ast.Expr)
        and isinstance(node.body[0].value, ast.Constant)
        and isinstance(node.body[0].value.value, str)
    ):
        return inspect.cleandoc(node.body[0].value.value)
    return None


def extract_enum_member_docstring(
    class_body: list[ast.stmt], index: int
) -> tuple[str | None, str | None]:
    """Extract docstring and deprecation from enum member.

    Returns
    -------
        Tuple of (description, deprecated)
    """
    next_idx = index + 1
    if next_idx >= len(class_body):
        return None, None

    next_stmt = class_body[next_idx]
    if not (
        isinstance(next_stmt, ast.Expr)
        and isinstance(next_stmt.value, ast.Constant)
        and isinstance(next_stmt.value.value, str)
    ):
        return None, None

    doc_text = next_stmt.value.value.strip()

    # Parse for description and deprecated directive
    description_lines: list[str] = []
    deprecated_lines: list[str] = []
    lines = doc_text.splitlines()
    it = iter(enumerate(lines))

    for _, raw_line in it:
        stripped = raw_line.strip()
        if stripped.startswith(".. deprecated::"):
            # Capture first line after the directive
            remainder = stripped[len(".. deprecated::") :].strip()
            if remainder:
                deprecated_lines.append(remainder)
            # Grab any indented continuation lines
            for _, cont in it:
                cont_stripped = cont.strip()
                if not cont_stripped:
                    continue
                if cont.startswith(("   ", "\t")):
                    deprecated_lines.append(cont_stripped)
                    continue
                # Hit a non-indented line: feed it back into description
                description_lines.append(cont_stripped)
                break
        else:
            description_lines.append(stripped)

    description = "\n".join(line for line in description_lines if line).strip()
    deprecated = "\n".join(line for line in deprecated_lines if line).strip()

    return description or None, deprecated or None


def parse_method(node: ast.FunctionDef | ast.AsyncFunctionDef) -> MethodInfo:
    """Parse a method definition from AST."""
    is_function = False
    is_async = isinstance(node, ast.AsyncFunctionDef)
    is_classmethod = False
    deprecated = None
    function_name = None
    cache = None
    check = False

    # Check for @function, @classmethod, and @check decorators
    for decorator in node.decorator_list:
        # Check if it's a simple Name node for @classmethod or @check
        if isinstance(decorator, ast.Name):
            if decorator.id == "classmethod":
                is_classmethod = True
                continue
            if decorator.id == "check":
                check = True
                continue

        dec_name, dec_kwargs = parse_decorator_call(decorator)
        if dec_name == "function":
            is_function = True
            deprecated = dec_kwargs.get("deprecated")
            function_name = dec_kwargs.get("name")
            cache = dec_kwargs.get("cache")
        elif dec_name == "check":
            check = True

    # Parse parameters (regular positional/keyword arguments)
    parameters = []
    for arg in node.args.args:
        # Skip self or cls
        if arg.arg in ("self", "cls"):
            continue

        # Extract Annotated metadata if present
        metadata = parse_annotated_metadata(arg.annotation)

        param_info = ParamInfo(
            name=arg.arg,
            type_annotation=get_type_annotation(arg.annotation),
            has_default=False,
            default_value=None,
            doc=metadata["doc"],
            api_name=metadata["api_name"],
            default_path=metadata["default_path"],
            ignore=metadata["ignore"],
            deprecated=metadata["deprecated"],
        )
        parameters.append(param_info)

    # Handle defaults for regular args
    num_defaults = len(node.args.defaults)
    if num_defaults > 0:
        # Defaults align with the last N parameters
        for i in range(num_defaults):
            param_idx = len(parameters) - num_defaults + i
            if param_idx >= 0:
                parameters[param_idx].has_default = True
                try:
                    # Try to evaluate the default value
                    parameters[param_idx].default_value = ast.literal_eval(
                        node.args.defaults[i]
                    )
                except Exception:  # noqa: BLE001
                    # If not a literal, store as unparsed string
                    parameters[param_idx].default_value = ast.unparse(
                        node.args.defaults[i]
                    )

    # Parse keyword-only parameters (after * in signature)
    for i, arg in enumerate(node.args.kwonlyargs):
        # Extract Annotated metadata if present
        metadata = parse_annotated_metadata(arg.annotation)

        # Check if this kwonly arg has a default (kw_defaults can contain None for no default)
        has_default = False
        default_value = None
        if i < len(node.args.kw_defaults) and node.args.kw_defaults[i] is not None:
            has_default = True
            try:
                default_value = ast.literal_eval(node.args.kw_defaults[i])
            except Exception:  # noqa: BLE001
                default_value = ast.unparse(node.args.kw_defaults[i])

        param_info = ParamInfo(
            name=arg.arg,
            type_annotation=get_type_annotation(arg.annotation),
            has_default=has_default,
            default_value=default_value,
            is_keyword_only=True,  # Mark as keyword-only
            doc=metadata["doc"],
            api_name=metadata["api_name"],
            default_path=metadata["default_path"],
            ignore=metadata["ignore"],
            deprecated=metadata["deprecated"],
        )
        parameters.append(param_info)

    # Skip varargs (*args) and kwargs (**kwargs) - they're not supported in Dagger
    # and would cause type resolution errors

    return MethodInfo(
        name=node.name,
        docstring=extract_docstring(node),
        is_function=is_function,
        is_async=is_async,
        is_classmethod=is_classmethod,
        parameters=parameters,
        return_type=get_type_annotation(node.returns),
        deprecated=deprecated,
        function_name=function_name,
        cache=cache,
        check=check,
    )


def parse_class(node: ast.ClassDef) -> ClassInfo:  # noqa: C901, PLR0912, PLR0915
    """Parse a class definition from AST."""
    decorator_type = None
    deprecated = None

    # Check for decorators
    for decorator in node.decorator_list:
        dec_name, dec_kwargs = parse_decorator_call(decorator)
        if dec_name == "object_type":
            decorator_type = "object_type"
            deprecated = dec_kwargs.get("deprecated")
        elif dec_name == "interface":
            decorator_type = "interface"
        elif dec_name == "enum_type":
            decorator_type = "enum_type"

    # Extract bases
    bases = []
    for base in node.bases:
        if isinstance(base, ast.Name):
            bases.append(base.id)
        elif isinstance(base, ast.Attribute):
            bases.append(f"{ast.unparse(base.value)}.{base.attr}")

    # Check if it's an enum
    is_enum = "Enum" in bases or any("enum.Enum" in base for base in bases)

    # Parse fields and methods
    fields = {}
    methods = {}
    enum_members = {}
    init_params: set[str] | None = None  # Track __init__ parameters if present
    init_method: MethodInfo | None = None  # Track full __init__ method if present

    for i, item in enumerate(node.body):
        if isinstance(item, ast.AnnAssign) and isinstance(item.target, ast.Name):
            # Field with type annotation
            field_name = item.target.id
            type_annotation = get_type_annotation(item.annotation)

            # Check if it's a dagger field (has field() as value)
            is_dagger_field = False
            field_deprecated = None
            field_alt_name = None
            field_init = True  # Default to True
            field_init_explicit = False  # Track if init was explicitly set
            field_default_explicit = False  # Track if default was explicitly set
            default_value = None
            field_default = None

            if item.value:
                # Check if the value is a call to field()
                if isinstance(item.value, ast.Call):
                    # Could be field() or dagger.field()
                    is_field_call = False
                    if (
                        isinstance(item.value.func, ast.Name)
                        and item.value.func.id == "field"
                    ) or (
                        isinstance(item.value.func, ast.Attribute)
                        and item.value.func.attr == "field"
                    ):
                        is_field_call = True

                    if is_field_call:
                        is_dagger_field = True
                        # Extract field kwargs
                        for keyword in item.value.keywords:
                            if keyword.arg == "deprecated":
                                try:
                                    field_deprecated = ast.literal_eval(keyword.value)
                                except (ValueError, TypeError, SyntaxError):
                                    # If not a literal, store unparsed expression
                                    field_deprecated = ast.unparse(keyword.value)
                                    logger.warning(
                                        "Field %s deprecated value is not a literal: %s",
                                        field_name,
                                        field_deprecated,
                                    )
                            elif keyword.arg == "name":
                                try:
                                    field_alt_name = ast.literal_eval(keyword.value)
                                except (ValueError, TypeError, SyntaxError):
                                    field_alt_name = ast.unparse(keyword.value)
                                    logger.warning(
                                        "Field %s name value is not a literal: %s",
                                        field_name,
                                        field_alt_name,
                                    )
                            elif keyword.arg == "init":
                                field_init_explicit = True
                                try:
                                    field_init = ast.literal_eval(keyword.value)
                                except (ValueError, TypeError, SyntaxError):
                                    logger.warning(
                                        "Field %s init value is not a boolean literal",
                                        field_name,
                                    )
                                    # Keep default value
                            elif keyword.arg == "default":
                                field_default_explicit = True  # Mark that default was explicitly set
                                # Extract default value from field()
                                # Try literal_eval first for simple values
                                try:
                                    field_default = ast.literal_eval(keyword.value)
                                except (ValueError, TypeError):
                                    # For non-literals (like `list`, `dict`, function names),
                                    # store the unparsed expression to be evaluated later
                                    # in the namespace with actual objects
                                    field_default = ast.unparse(keyword.value)
                    else:
                        # Regular default value (not a field() call)
                        with contextlib.suppress(Exception):
                            default_value = ast.literal_eval(item.value)
                else:
                    # Simple default value (not a call)
                    with contextlib.suppress(Exception):
                        default_value = ast.literal_eval(item.value)

            # Determine if there's a default value
            # For dagger fields: only if field() has a default= kwarg explicitly set
            # For regular fields: if there's any assignment
            if is_dagger_field:
                has_default = field_default_explicit
                actual_default = field_default
            else:
                has_default = item.value is not None
                actual_default = default_value

            fields[field_name] = FieldInfo(
                name=field_name,
                type_annotation=type_annotation,
                has_default=has_default,
                default_value=actual_default,
                is_dagger_field=is_dagger_field,
                deprecated=field_deprecated,
                field_name=field_alt_name,
                init=field_init,
                init_explicit=field_init_explicit,
                default_explicit=field_default_explicit,
            )

        elif isinstance(item, ast.Assign) and is_enum:
            # Enum member
            for target in item.targets:
                if isinstance(target, ast.Name):
                    member_name = target.id
                    member_value = None

                    with contextlib.suppress(Exception):
                        member_value = ast.literal_eval(item.value)

                    # Extract docstring and deprecation from next statement
                    description, deprecated = extract_enum_member_docstring(node.body, i)

                    enum_members[member_name] = (member_value, description, deprecated)

        elif isinstance(item, (ast.FunctionDef, ast.AsyncFunctionDef)):
            # Parse __init__ to determine constructor parameters
            if item.name == "__init__":
                # Parse the full __init__ method
                init_method = parse_method(item)
                # Extract parameter names from __init__ (excluding self)
                init_params = {
                    arg.arg for arg in item.args.args if arg.arg != "self"
                }
            elif not item.name.startswith("_"):  # Skip other private methods
                method_info = parse_method(item)
                methods[item.name] = method_info

    return ClassInfo(
        name=node.name,
        bases=bases,
        docstring=extract_docstring(node),
        decorator_type=decorator_type,
        deprecated=deprecated,
        fields=fields,
        methods=methods,
        is_enum=is_enum,
        enum_members=enum_members,
        init_params=init_params,
        init_method=init_method,
    )


def analyze_source_file(file_path: Path) -> tuple[dict[str, ClassInfo], str | None]:
    """Analyze a Python source file and extract class information.

    Returns
    -------
        Tuple of (classes_dict, module_docstring)
    """
    try:
        source = file_path.read_text(encoding="utf-8")
        tree = ast.parse(source, filename=str(file_path))
    except Exception as e:  # noqa: BLE001
        logger.warning("Failed to parse %s: %s", file_path, e)
        return {}, None

    # Extract module docstring
    module_docstring = extract_docstring(tree)

    classes = {}
    for node in ast.walk(tree):
        if isinstance(node, ast.ClassDef):
            class_info = parse_class(node)
            # Only include classes with dagger decorators or enums with decorator
            if class_info.decorator_type or (
                class_info.is_enum and class_info.decorator_type
            ):
                classes[class_info.name] = class_info

    return classes, module_docstring


def create_safe_namespace() -> dict[str, Any]:
    """Create a namespace for evaluating type annotations safely.

    This includes common types and creates mock objects for missing imports.
    """
    import builtins
    from typing import Annotated, Self

    namespace = {
        "__builtins__": builtins,
        "typing": typing,
        "Self": Self,
        "Annotated": Annotated,
        "str": str,
        "int": int,
        "bool": bool,
        "float": float,
        "list": list,
        "dict": dict,
        "tuple": tuple,
        "set": set,
        "None": None,
        "Any": typing.Any,
        "Optional": typing.Optional,
        "Union": typing.Union,
        "List": list,
        "Dict": dict,
        "Tuple": tuple,
        "Set": set,
    }

    # Add dagger types
    try:
        import dagger

        namespace["dagger"] = dagger
        # Add commonly used dagger types
        for attr_name in dir(dagger):
            attr = getattr(dagger, attr_name)
            if isinstance(attr, type):
                namespace[attr_name] = attr

        # Add commonly used dagger metadata/decorators that users import directly
        from dagger import DefaultPath, Deprecated, Doc, Ignore, Name

        namespace["Doc"] = Doc
        namespace["DefaultPath"] = DefaultPath
        namespace["Ignore"] = Ignore
        namespace["Name"] = Name
        namespace["Deprecated"] = Deprecated
    except ImportError:
        pass

    return namespace


def evaluate_annotation(annotation_str: str | None, namespace: dict[str, Any]) -> Any:
    """Safely evaluate a type annotation string.

    Falls back to string annotation if evaluation fails.
    """
    if annotation_str is None:
        return None

    try:
        # Try to evaluate the annotation
        return eval(annotation_str, namespace)  # noqa: S307
    except Exception as e:  # noqa: BLE001
        logger.debug("Could not evaluate annotation '%s': %s", annotation_str, e)
        # Return as string - will be handled by type system
        return annotation_str


def build_mock_class_from_info(  # noqa: C901, PLR0912
    class_info: ClassInfo, namespace: dict[str, Any], mod: Module
) -> type:
    """Build a mock class from AST-extracted information.

    This creates a dataclass with the necessary attributes for dagger to process it.
    """
    # Build class attributes
    annotations = {}
    class_attrs: dict[str, Any] = {
        "__module__": "__dagger_mock__",  # Use a custom module name
        "__doc__": class_info.docstring,
        "__annotations__": annotations,
    }

    # Add fields as class attributes
    for field_name, field_info in class_info.fields.items():
        if field_info.type_annotation:
            # Evaluate the annotation to get actual type
            evaluated_type = evaluate_annotation(field_info.type_annotation, namespace)
            # Store the evaluated type, not string
            annotations[field_name] = evaluated_type

        if field_info.is_dagger_field:
            # Create a field descriptor with mod.field()
            field_kwargs = {}
            if field_info.has_default:
                # Use the actual default value extracted from AST
                # If it's a string, it might be an unparsed expression (e.g., "list")
                # that we need to evaluate in the namespace
                default_val = field_info.default_value
                if isinstance(default_val, str):
                    # Try to evaluate it in the namespace to resolve callables like list, dict
                    # For actual string literals from literal_eval, eval will fail and we keep the string
                    try:
                        evaluated = eval(default_val, namespace)  # noqa: S307
                        # Only use evaluated result if it's callable or a type
                        # This distinguishes "list" (unparsed) from "hello" (actual string)
                        if callable(evaluated) or isinstance(evaluated, type):
                            default_val = evaluated
                    except Exception:  # noqa: BLE001
                        # If evaluation fails, keep the string value
                        # For string literals from literal_eval, this preserves the string
                        pass
                field_kwargs["default"] = default_val
            if field_info.deprecated is not None:
                field_kwargs["deprecated"] = field_info.deprecated
            if field_info.field_name:
                field_kwargs["name"] = field_info.field_name

            # Determine if field should be in constructor
            # Priority: explicit init= > __init__ parameters > default True
            if field_info.init_explicit:
                # User explicitly set init= in field(), respect it
                if not field_info.init:
                    field_kwargs["init"] = False
            elif class_info.init_params is not None:
                # There's an explicit __init__, only include fields that are parameters
                if field_name not in class_info.init_params:
                    field_kwargs["init"] = False
            # else: default is init=True (field is in constructor)

            class_attrs[field_name] = mod.field(**field_kwargs)
        elif field_info.has_default:
            # Regular field with default value - use the actual default
            class_attrs[field_name] = field_info.default_value
        # Note: Fields without defaults and without field() are just type annotations
        # The dataclass decorator will handle them

    # Add methods
    for method_name, method_info in class_info.methods.items():
        # Create a mock function
        # We need to create a function with the right signature
        mock_func = create_mock_method(method_info, namespace)

        # Set @check attribute before applying @function decorator
        if method_info.check:
            setattr(mock_func, CHECK_DEF_KEY, True)

        if method_info.is_function:
            # Wrap with @function decorator
            func_kwargs = {}
            if method_info.deprecated is not None:
                func_kwargs["deprecated"] = method_info.deprecated
            if method_info.function_name:
                func_kwargs["name"] = method_info.function_name
            if method_info.cache:
                func_kwargs["cache"] = method_info.cache

            mock_func = mod.function(mock_func, **func_kwargs)

        # Wrap with @classmethod if needed
        if method_info.is_classmethod:
            mock_func = classmethod(mock_func)

        class_attrs[method_name] = mock_func

    # Handle __init__ method
    if class_info.decorator_type != "interface":
        if class_info.init_method:
            # User provided explicit __init__, create a mock version with same signature
            # This prevents dataclass from auto-generating __init__
            mock_init = create_mock_method(class_info.init_method, namespace)
            class_attrs["__init__"] = mock_init
        elif not class_info.fields:
            # No fields and no __init__, add minimal constructor
            def __init__(self):  # noqa: N807
                pass

            class_attrs["__init__"] = __init__
        # else: let dataclass decorator generate __init__ from fields

    # Create the class using type()
    # For interfaces, inherit from typing.Protocol
    # Don't override __init__ for interfaces - the module system will handle it
    if class_info.decorator_type == "interface":
        mock_cls = type(class_info.name, (typing.Protocol,), class_attrs)
    else:
        mock_cls = type(class_info.name, (), class_attrs)

    # Apply the appropriate decorator
    if class_info.decorator_type == "object_type":
        decorator_kwargs = {}
        if class_info.deprecated is not None:
            decorator_kwargs["deprecated"] = class_info.deprecated
        mock_cls = mod.object_type(mock_cls, **decorator_kwargs)
    elif class_info.decorator_type == "interface":
        mock_cls = mod.interface(mock_cls)
    elif class_info.decorator_type == "enum_type" and class_info.is_enum:
        # Create enum dynamically
        mock_cls = create_mock_enum(class_info, mod)

    return mock_cls


def create_mock_method(method_info: MethodInfo, namespace: dict[str, Any]) -> Any:
    """Create a mock method with the right signature."""
    # Build parameter list for function signature
    # Use 'cls' for classmethods, 'self' for regular methods
    first_param_name = "cls" if method_info.is_classmethod else "self"
    params = [
        inspect.Parameter(first_param_name, inspect.Parameter.POSITIONAL_OR_KEYWORD)
    ]

    # Build annotations dict for get_type_hints()
    annotations = {}

    for param_info in method_info.parameters:
        # Use actual default value if available
        if param_info.has_default:
            # Use the actual default value from AST
            default = (
                param_info.default_value
                if param_info.default_value is not None
                else None
            )
        else:
            default = inspect.Parameter.empty

        # Evaluate base annotation
        base_annotation = (
            evaluate_annotation(param_info.type_annotation, namespace)
            if param_info.type_annotation
            else inspect.Parameter.empty
        )

        # Wrap with Annotated if we have metadata
        annotation = base_annotation
        if base_annotation is not inspect.Parameter.empty:
            metadata_items = []

            # Add metadata in the correct order
            if param_info.doc is not None:
                Doc = namespace.get("Doc")  # noqa: N806
                if Doc:
                    metadata_items.append(Doc(param_info.doc))

            if param_info.api_name is not None:
                Name = namespace.get("Name")  # noqa: N806
                if Name:
                    metadata_items.append(Name(param_info.api_name))

            if param_info.default_path is not None:
                DefaultPath = namespace.get("DefaultPath")  # noqa: N806
                if DefaultPath:
                    metadata_items.append(DefaultPath(param_info.default_path))

            if param_info.ignore is not None:
                Ignore = namespace.get("Ignore")  # noqa: N806
                if Ignore:
                    metadata_items.append(Ignore(param_info.ignore))

            if param_info.deprecated is not None:
                Deprecated = namespace.get("Deprecated")  # noqa: N806
                if Deprecated:
                    if param_info.deprecated:
                        metadata_items.append(Deprecated(param_info.deprecated))
                    else:
                        metadata_items.append(Deprecated())

            # If we have metadata, wrap in Annotated
            if metadata_items:
                Annotated = namespace.get("Annotated")  # noqa: N806
                if Annotated:
                    annotation = Annotated[base_annotation, *metadata_items]

        # Use correct parameter kind based on whether it's keyword-only
        param_kind = (
            inspect.Parameter.KEYWORD_ONLY
            if param_info.is_keyword_only
            else inspect.Parameter.POSITIONAL_OR_KEYWORD
        )

        param = inspect.Parameter(
            param_info.name,
            param_kind,
            default=default,
            annotation=annotation,
        )
        params.append(param)

        # Store annotation for __annotations__
        if annotation is not inspect.Parameter.empty:
            annotations[param_info.name] = annotation

    # Return annotation
    return_annotation = (
        evaluate_annotation(method_info.return_type, namespace)
        if method_info.return_type
        else inspect.Parameter.empty
    )

    if return_annotation is not inspect.Parameter.empty:
        annotations["return"] = return_annotation

    # Create signature
    sig = inspect.Signature(params, return_annotation=return_annotation)

    # Build a function dynamically with the exact parameters needed
    # This avoids the issue of *args/**kwargs being detected as untyped parameters
    param_names = [p.name for p in params]
    param_str = ", ".join(param_names)

    # Use a unique temporary name to avoid collisions in the namespace
    import uuid

    temp_name = f"_mock_{method_info.name}_{uuid.uuid4().hex[:8]}"

    if method_info.is_async:
        func_code = f"async def {temp_name}({param_str}):\n    pass"
    else:
        func_code = f"def {temp_name}({param_str}):\n    pass"

    # Execute the function definition in a temporary namespace
    temp_namespace = {}
    exec(func_code, namespace, temp_namespace)  # noqa: S102
    mock_method = temp_namespace[temp_name]

    # Set the correct name, signature, and annotations
    mock_method.__name__ = method_info.name
    mock_method.__qualname__ = method_info.name
    mock_method.__signature__ = sig  # type: ignore[assignment]
    mock_method.__annotations__ = annotations  # type: ignore[assignment]
    if method_info.docstring:
        mock_method.__doc__ = method_info.docstring

    return mock_method


def create_mock_enum(class_info: ClassInfo, mod: Module) -> type:
    """Create a mock enum class."""
    # Build enum members
    enum_dict = {}
    member_metadata: dict[str, tuple[str | None, str | None]] = {}

    for member_name, (member_value, description, deprecated) in class_info.enum_members.items():
        if member_value is None:
            member_value = member_name  # Default value  # noqa: PLW2901
        enum_dict[member_name] = member_value
        member_metadata[member_name] = (description, deprecated)

    if not enum_dict:
        # If no members found, create a dummy one
        enum_dict["PLACEHOLDER"] = "PLACEHOLDER"

    # Create enum class
    mock_enum = enum.Enum(class_info.name, enum_dict)  # type: ignore[misc]

    # Set class docstring
    if class_info.docstring:
        mock_enum.__doc__ = class_info.docstring

    # Set member descriptions and deprecation
    for member_name, (description, deprecated) in member_metadata.items():
        if member_name in mock_enum.__members__:
            member = mock_enum[member_name]
            if description:
                # Set member docstring (not all enum implementations support this)
                try:
                    member.__doc__ = description  # type: ignore[misc]
                except (AttributeError, TypeError):
                    pass
            if deprecated is not None:
                # Set deprecation as attribute (use None check to handle empty strings)
                try:
                    member.deprecated = deprecated  # type: ignore[attr-defined]
                except (AttributeError, TypeError):
                    pass

    # Apply decorator
    return mod.enum_type(mock_enum)


def load_module_from_ast(  # noqa: C901, PLR0912, PLR0915
    main_name: str | None = None, module_path: Path | None = None
) -> Module:
    """Load a module by analyzing source code without executing it.

    Args:
        main_name: Name of the main object. If None, will try to get
            from entry point.
        module_path: Path to the module file or directory. If None, will
            try to find from entry point.
    """
    try:
        if main_name is None:
            main_name = MAIN_OBJECT

        if not main_name:
            # Try to get from entry point
            ep = get_entry_point()
            main_name = ep.value.split(":")[1] if ":" in ep.value else ""

        logger.debug("AST loader: main_name=%s, module_path=%s", main_name, module_path)

        # Find all module files
        module_files = find_module_files(module_path)
        logger.debug("AST loader: found %d files to parse", len(module_files))
    except Exception as e:
        logger.exception("Failed to initialize AST loader")
        msg = f"Failed to initialize AST loader: {e}"
        raise ModuleLoadError(msg) from e

    # Parse all files to extract class information
    all_classes: dict[str, ClassInfo] = {}
    module_docstrings: dict[Path, str] = {}
    try:
        for file_path in module_files:
            logger.debug("AST loader: parsing %s", file_path)
            classes, module_docstring = analyze_source_file(file_path)
            all_classes.update(classes)
            if module_docstring:
                module_docstrings[file_path] = module_docstring
            logger.debug("AST loader: found %d classes in %s", len(classes), file_path)
    except Exception as e:
        logger.exception("Failed to parse source files")
        msg = f"Failed to parse source files: {e}"
        raise ModuleLoadError(msg) from e

    logger.debug("AST loader: total classes found: %s", list(all_classes.keys()))

    # Create module instance
    try:
        mod = Module(main_name=main_name)
        logger.debug("AST loader: created Module instance with main_name=%s", main_name)

        # Set module description from collected docstrings
        # Prefer __init__.py docstring, otherwise use the first non-empty one
        if module_docstrings:
            # Look for __init__.py first
            init_docstring = None
            for file_path, docstring in module_docstrings.items():
                if file_path.name == "__init__.py":
                    init_docstring = docstring
                    break

            if init_docstring:
                mod._module_description = init_docstring  # noqa: SLF001
                logger.debug("AST loader: using __init__.py docstring as module description")
            else:
                # Use the first non-empty docstring
                mod._module_description = next(iter(module_docstrings.values()))  # noqa: SLF001
                logger.debug("AST loader: using first docstring as module description")
    except Exception as e:
        logger.exception("Failed to create Module instance")
        msg = f"Failed to create Module instance: {e}"
        raise ModuleLoadError(msg) from e

    # Create namespace for evaluating type annotations
    try:
        namespace = create_safe_namespace()
        logger.debug("AST loader: created safe namespace")
    except Exception as e:
        logger.exception("Failed to create safe namespace")
        msg = f"Failed to create safe namespace: {e}"
        raise ModuleLoadError(msg) from e

    # Create a mock module in sys.modules so get_type_hints() can find it
    try:
        import types

        mock_module = types.ModuleType("__dagger_mock__")
        mock_module.__dict__.update(namespace)
        sys.modules["__dagger_mock__"] = mock_module
        logger.debug("AST loader: created mock module in sys.modules")
    except Exception as e:
        logger.exception("Failed to create mock module")
        msg = f"Failed to create mock module: {e}"
        raise ModuleLoadError(msg) from e

    # First pass: create placeholder classes for forward references
    for class_name in all_classes:
        # Create a minimal placeholder class
        namespace[class_name] = type(class_name, (), {})
        # Also add to mock module so forward references work
        setattr(mock_module, class_name, namespace[class_name])
    logger.debug("AST loader: created %d placeholder classes", len(all_classes))

    # Second pass: build actual mock classes and populate the module
    successful_classes = []
    failed_classes = []
    for class_name, class_info in all_classes.items():
        try:
            logger.debug("AST loader: building mock class for %s", class_name)
            # Build and replace the placeholder
            mock_cls = build_mock_class_from_info(class_info, namespace, mod)
            namespace[class_name] = mock_cls
            # Also update the mock module so forward references work
            setattr(mock_module, class_name, mock_cls)
            successful_classes.append(class_name)
            logger.debug("AST loader: successfully built %s", class_name)
        except Exception as e:  # noqa: PERF203
            failed_classes.append(class_name)
            logger.warning("Failed to build mock class for %s: %s", class_name, e)
            logger.exception("Exception details")
            continue

    logger.info(
        "AST loader: successfully built %d classes, failed %d",
        len(successful_classes),
        len(failed_classes),
    )
    if failed_classes:
        logger.warning("AST loader: failed classes: %s", failed_classes)

    if not successful_classes:
        msg = f"No classes were successfully loaded. Failed classes: {failed_classes}"
        logger.error(msg)
        raise ModuleLoadError(msg)

    # Auto-detect main object if not explicitly set
    if mod._main is None:  # noqa: SLF001
        logger.debug("AST loader: main object not set, attempting auto-detection")

        # Strategy 1: If there's only one object/interface, use it as main
        if len(mod._objects) == 1:  # noqa: SLF001
            main_obj = next(iter(mod._objects.values()))  # noqa: SLF001
            mod._main = main_obj  # noqa: SLF001
            logger.info(
                "AST loader: auto-detected main object (only one): %s",
                main_obj.cls.__name__,
            )
        else:
            # Strategy 2: Look for object with capitalized module name
            # e.g., for module "duck", look for class "Duck"
            module_name = os.getenv("DAGGER_MODULE", "")
            if module_name:
                capitalized_name = module_name.capitalize()
                if capitalized_name in mod._objects:  # noqa: SLF001
                    mod._main = mod._objects[capitalized_name]  # noqa: SLF001
                    logger.info(
                        "AST loader: auto-detected main object (by name): %s",
                        capitalized_name,
                    )

        if mod._main is None:  # noqa: SLF001
            available = list(mod._objects.keys())  # noqa: SLF001
            msg = (
                f"Could not auto-detect main object. Available: {available}. "
                "Set DAGGER_MAIN_OBJECT to specify main object."
            )
            logger.error(msg)
            raise ModuleLoadError(msg)

    return mod
