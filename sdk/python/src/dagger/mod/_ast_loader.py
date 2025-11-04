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
from dagger.mod._module import Module

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
    enum_members: dict[str, tuple[Any, str | None]] = dataclasses.field(
        default_factory=dict
    )


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


@dataclasses.dataclass
class ParamInfo:
    """Information about a parameter extracted from AST."""

    name: str
    type_annotation: str | None
    has_default: bool
    default_value: Any  # Can be any Python value including None


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


def parse_method(node: ast.FunctionDef | ast.AsyncFunctionDef) -> MethodInfo:
    """Parse a method definition from AST."""
    is_function = False
    is_async = isinstance(node, ast.AsyncFunctionDef)
    is_classmethod = False
    deprecated = None
    function_name = None
    cache = None

    # Check for @function and @classmethod decorators
    for decorator in node.decorator_list:
        # Check if it's a simple Name node for @classmethod
        if isinstance(decorator, ast.Name) and decorator.id == "classmethod":
            is_classmethod = True
            continue

        dec_name, dec_kwargs = parse_decorator_call(decorator)
        if dec_name == "function":
            is_function = True
            deprecated = dec_kwargs.get("deprecated")
            function_name = dec_kwargs.get("name")
            cache = dec_kwargs.get("cache")
            break

    # Parse parameters
    parameters = []
    for arg in node.args.args:
        # Skip self or cls
        if arg.arg in ("self", "cls"):
            continue

        param_info = ParamInfo(
            name=arg.arg,
            type_annotation=get_type_annotation(arg.annotation),
            has_default=False,
            default_value=None,
        )
        parameters.append(param_info)

    # Handle defaults
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

    for item in node.body:
        if isinstance(item, ast.AnnAssign) and isinstance(item.target, ast.Name):
            # Field with type annotation
            field_name = item.target.id
            type_annotation = get_type_annotation(item.annotation)

            # Check if it's a dagger field (has field() as value)
            is_dagger_field = False
            field_deprecated = None
            field_alt_name = None
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
                                with contextlib.suppress(Exception):
                                    field_deprecated = ast.literal_eval(keyword.value)
                            elif keyword.arg == "name":
                                with contextlib.suppress(Exception):
                                    field_alt_name = ast.literal_eval(keyword.value)
                            elif keyword.arg == "default":
                                # Extract default value from field()
                                with contextlib.suppress(Exception):
                                    field_default = ast.literal_eval(keyword.value)
                    else:
                        # Regular default value (not a field() call)
                        with contextlib.suppress(Exception):
                            default_value = ast.literal_eval(item.value)
                else:
                    # Simple default value (not a call)
                    with contextlib.suppress(Exception):
                        default_value = ast.literal_eval(item.value)

            # Use field_default if it's a dagger field, otherwise use default_value
            actual_default = field_default if is_dagger_field else default_value

            fields[field_name] = FieldInfo(
                name=field_name,
                type_annotation=type_annotation,
                has_default=item.value is not None,
                default_value=actual_default,
                is_dagger_field=is_dagger_field,
                deprecated=field_deprecated,
                field_name=field_alt_name,
            )

        elif isinstance(item, ast.Assign) and is_enum:
            # Enum member
            for target in item.targets:
                if isinstance(target, ast.Name):
                    member_name = target.id
                    member_value = None
                    member_doc = None

                    with contextlib.suppress(Exception):
                        member_value = ast.literal_eval(item.value)

                    # Look for docstring after the assignment
                    # (This is a simplified approach; proper enum
                    # docstrings are more complex)

                    enum_members[member_name] = (member_value, member_doc)

        elif isinstance(item, (ast.FunctionDef, ast.AsyncFunctionDef)):
            if not item.name.startswith("_"):  # Skip private methods
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
    )


def analyze_source_file(file_path: Path) -> dict[str, ClassInfo]:
    """Analyze a Python source file and extract class information."""
    try:
        source = file_path.read_text(encoding="utf-8")
        tree = ast.parse(source, filename=str(file_path))
    except Exception as e:  # noqa: BLE001
        logger.warning("Failed to parse %s: %s", file_path, e)
        return {}

    classes = {}
    for node in ast.walk(tree):
        if isinstance(node, ast.ClassDef):
            class_info = parse_class(node)
            # Only include classes with dagger decorators or enums with decorator
            if class_info.decorator_type or (
                class_info.is_enum and class_info.decorator_type
            ):
                classes[class_info.name] = class_info

    return classes


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
                field_kwargs["default"] = field_info.default_value
            if field_info.deprecated:
                field_kwargs["deprecated"] = field_info.deprecated
            if field_info.field_name:
                field_kwargs["name"] = field_info.field_name

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

        if method_info.is_function:
            # Wrap with @function decorator
            func_kwargs = {}
            if method_info.deprecated:
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

    # Create the class using type()
    mock_cls = type(class_info.name, (), class_attrs)

    # Apply the appropriate decorator
    if class_info.decorator_type == "object_type":
        decorator_kwargs = {}
        if class_info.deprecated:
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

        annotation = (
            evaluate_annotation(param_info.type_annotation, namespace)
            if param_info.type_annotation
            else inspect.Parameter.empty
        )

        param = inspect.Parameter(
            param_info.name,
            inspect.Parameter.POSITIONAL_OR_KEYWORD,
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

    # Create a placeholder function with the namespace as globals
    # This is important so that get_type_hints() can resolve the annotations
    import types

    if method_info.is_async:

        async def _temp_method(self, *args, **kwargs):
            """Mock method placeholder."""

        # Recreate with proper globals
        mock_method = types.FunctionType(
            _temp_method.__code__,
            namespace,  # Use the namespace as globals!
            method_info.name,
            None,  # argdefs
            _temp_method.__closure__,
        )
    else:

        def _temp_method(self, *args, **kwargs):
            """Mock method placeholder."""

        # Recreate with proper globals
        mock_method = types.FunctionType(
            _temp_method.__code__,
            namespace,  # Use the namespace as globals!
            method_info.name,
            None,  # argdefs
            _temp_method.__closure__,
        )

    mock_method.__signature__ = sig  # type: ignore[assignment]
    mock_method.__annotations__ = annotations  # type: ignore[assignment]
    if method_info.docstring:
        mock_method.__doc__ = method_info.docstring

    return mock_method


def create_mock_enum(class_info: ClassInfo, mod: Module) -> type:
    """Create a mock enum class."""
    # Build enum members
    enum_dict = {}
    for member_name, (member_value, _member_doc) in class_info.enum_members.items():
        if member_value is None:
            member_value = member_name  # Default value  # noqa: PLW2901
        enum_dict[member_name] = member_value

    if not enum_dict:
        # If no members found, create a dummy one
        enum_dict["PLACEHOLDER"] = "PLACEHOLDER"

    # Create enum class
    mock_enum = enum.Enum(class_info.name, enum_dict)  # type: ignore[misc]

    # Set docstring
    if class_info.docstring:
        mock_enum.__doc__ = class_info.docstring

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
    try:
        for file_path in module_files:
            logger.debug("AST loader: parsing %s", file_path)
            classes = analyze_source_file(file_path)
            all_classes.update(classes)
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

    return mod
