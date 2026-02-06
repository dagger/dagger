"""Visitor for extracting decorator information from AST."""

from __future__ import annotations

import ast
import dataclasses
from typing import Any


@dataclasses.dataclass
class DecoratorInfo:
    """Information extracted from a decorator."""

    name: str  # e.g., "function", "object_type", "dagger.function"
    args: list[Any] = dataclasses.field(default_factory=list)
    kwargs: dict[str, Any] = dataclasses.field(default_factory=dict)
    node: ast.expr | None = None  # The original AST node


# Known Dagger decorators and their possible names
DAGGER_DECORATORS = {
    # @dagger.object_type or @mod.object_type or @object_type
    "object_type": {"object_type", "dagger.object_type", "mod.object_type"},
    # @dagger.function or @mod.function or @function
    "function": {"function", "dagger.function", "mod.function"},
    # @dagger.field or @mod.field or @field
    "field": {"field", "dagger.field", "mod.field"},
    # @dagger.interface or @mod.interface or @interface
    "interface": {"interface", "dagger.interface", "mod.interface"},
    # @dagger.enum_type or @mod.enum_type or @enum_type
    "enum_type": {"enum_type", "dagger.enum_type", "mod.enum_type"},
    # @dagger.check or @mod.check or @check
    "check": {"check", "dagger.check", "mod.check"},
}


def get_decorator_name(decorator: ast.expr) -> str:
    """Get the name of a decorator.

    Handles:
    - Simple names: @foo
    - Attribute access: @mod.foo
    - Call expressions: @foo() or @mod.foo()
    """
    if isinstance(decorator, ast.Call):
        return get_decorator_name(decorator.func)
    if isinstance(decorator, ast.Name):
        return decorator.id
    if isinstance(decorator, ast.Attribute):
        # e.g., mod.function -> "mod.function"
        value_name = get_decorator_name(decorator.value)
        return f"{value_name}.{decorator.attr}"
    return ""


def has_decorator(
    node: ast.ClassDef | ast.FunctionDef | ast.AsyncFunctionDef,
    decorator_type: str,
) -> bool:
    """Check if a node has a specific Dagger decorator.

    Args:
        node: The AST node to check.
        decorator_type: One of "object_type", "function", "field", etc.

    Returns
    -------
        True if the node has the decorator.
    """
    if decorator_type not in DAGGER_DECORATORS:
        return False

    valid_names = DAGGER_DECORATORS[decorator_type]

    for decorator in node.decorator_list:
        name = get_decorator_name(decorator)
        if name in valid_names:
            return True

    return False


def find_decorator(
    node: ast.ClassDef | ast.FunctionDef | ast.AsyncFunctionDef, decorator_type: str
) -> ast.expr | None:
    """Find a specific Dagger decorator on a node.

    Args:
        node: The AST node to check.
        decorator_type: One of "object_type", "function", "field", etc.

    Returns
    -------
        The decorator AST node, or None if not found.
    """
    if decorator_type not in DAGGER_DECORATORS:
        return None

    valid_names = DAGGER_DECORATORS[decorator_type]

    for decorator in node.decorator_list:
        name = get_decorator_name(decorator)
        if name in valid_names:
            return decorator

    return None


def extract_decorator_info(decorator: ast.expr) -> DecoratorInfo:
    """Extract information from a decorator AST node.

    Handles:
    - @decorator
    - @decorator()
    - @decorator(arg1, arg2)
    - @decorator(key=value)
    """
    name = get_decorator_name(decorator)
    args: list[Any] = []
    kwargs: dict[str, Any] = {}

    if isinstance(decorator, ast.Call):
        # Extract positional arguments
        args = [_eval_decorator_arg(arg) for arg in decorator.args]

        # Extract keyword arguments
        kwargs = {
            keyword.arg: _eval_decorator_arg(keyword.value)
            for keyword in decorator.keywords
            if keyword.arg is not None
        }

    return DecoratorInfo(name=name, args=args, kwargs=kwargs, node=decorator)


def _eval_decorator_arg(node: ast.expr) -> Any:  # noqa: PLR0911
    """Evaluate a decorator argument to a Python value.

    Only handles simple literal values. Complex expressions
    are returned as strings.
    """
    if isinstance(node, ast.Constant):
        return node.value

    if isinstance(node, ast.Name):
        name_map = {"True": True, "False": False, "None": None}
        return name_map.get(node.id, node.id)

    if isinstance(node, ast.List):
        return [_eval_decorator_arg(el) for el in node.elts]

    if isinstance(node, ast.Tuple):
        return tuple(_eval_decorator_arg(el) for el in node.elts)

    if isinstance(node, ast.Dict):
        return {
            _eval_decorator_arg(k) if k else None: _eval_decorator_arg(v)
            for k, v in zip(node.keys, node.values, strict=False)
        }

    if isinstance(node, ast.UnaryOp) and isinstance(node.op, ast.USub):
        val = _eval_decorator_arg(node.operand)
        if isinstance(val, (int, float)):
            return -val

    # For complex expressions, return the source code
    return ast.unparse(node)


def is_classmethod(node: ast.FunctionDef | ast.AsyncFunctionDef) -> bool:
    """Check if a function has @classmethod decorator."""
    for decorator in node.decorator_list:
        name = get_decorator_name(decorator)
        if name == "classmethod":
            return True
    return False


def is_staticmethod(node: ast.FunctionDef | ast.AsyncFunctionDef) -> bool:
    """Check if a function has @staticmethod decorator."""
    for decorator in node.decorator_list:
        name = get_decorator_name(decorator)
        if name == "staticmethod":
            return True
    return False


def get_function_decorators(
    node: ast.FunctionDef | ast.AsyncFunctionDef,
) -> list[DecoratorInfo]:
    """Get all decorator info for a function."""
    return [extract_decorator_info(d) for d in node.decorator_list]
