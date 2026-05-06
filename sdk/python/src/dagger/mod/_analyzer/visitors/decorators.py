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


# Canonical names of dagger decorators (and ``field``, which is also matched
# through the same alias machinery for the ``x: T = field()`` shape).
DAGGER_DECORATOR_NAMES: frozenset[str] = frozenset(
    {
        "object_type",
        "function",
        "field",
        "interface",
        "enum_type",
        "check",
        "generate",
        "up",
    },
)


@dataclasses.dataclass
class DaggerAliases:
    """Per-file map of names bound to the dagger package and decorators.

    Captures everything an ``import``/``from`` chain in a single file can do
    to rename a dagger decorator: ``import dagger as d``, ``from dagger
    import object_type as ot``, ``from dagger import field as fld``.

    A default-constructed instance recognises only the unaliased forms
    (``dagger``, ``mod``, and bare ``object_type``/``function``/...), which
    matches the historical static behaviour.
    """

    # Names whose attribute access resolves to dagger.X — typically
    # ``{"dagger", "mod"}`` plus any aliases from ``import dagger as <x>``
    # or ``from dagger import mod as <x>``.
    package_aliases: set[str] = dataclasses.field(default_factory=set)

    # Bare names bound to a canonical dagger decorator. Always contains the
    # identity mapping for unaliased decorators, plus any ``from dagger
    # import X as Y`` entries.
    bare_decorators: dict[str, str] = dataclasses.field(default_factory=dict)

    # Bare names that resolve to ``dagger.field`` (used by the field-call
    # detection in the parser).
    bare_field_names: set[str] = dataclasses.field(default_factory=set)

    @classmethod
    def default(cls) -> DaggerAliases:
        """Static defaults — same names the analyzer historically matched."""
        return cls(
            package_aliases={"dagger", "mod"},
            bare_decorators={name: name for name in DAGGER_DECORATOR_NAMES},
            bare_field_names={"field"},
        )


def resolve_dagger_decorator(
    decorator: ast.expr,
    aliases: DaggerAliases,
) -> str | None:
    """Map a decorator AST node to its canonical dagger decorator name.

    Handles the call form (``@function()``) by recursing on ``.func``.
    Returns ``None`` when the decorator is not a dagger decorator under
    the given aliases.
    """
    if isinstance(decorator, ast.Call):
        return resolve_dagger_decorator(decorator.func, aliases)
    if isinstance(decorator, ast.Name):
        return aliases.bare_decorators.get(decorator.id)
    if (
        isinstance(decorator, ast.Attribute)
        and isinstance(decorator.value, ast.Name)
        and decorator.value.id in aliases.package_aliases
        and decorator.attr in DAGGER_DECORATOR_NAMES
    ):
        return decorator.attr
    return None


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
    aliases: DaggerAliases | None = None,
) -> bool:
    """Check if a node has a specific Dagger decorator.

    Args:
        node: The AST node to check.
        decorator_type: One of "object_type", "function", "field", etc.
        aliases: Per-file dagger import aliases. Defaults to the static
            unaliased forms when not provided.

    Returns
    -------
        True if the node has the decorator.
    """
    if decorator_type not in DAGGER_DECORATOR_NAMES:
        return False

    aliases = aliases or DaggerAliases.default()
    return any(
        resolve_dagger_decorator(d, aliases) == decorator_type
        for d in node.decorator_list
    )


def find_decorator(
    node: ast.ClassDef | ast.FunctionDef | ast.AsyncFunctionDef,
    decorator_type: str,
    aliases: DaggerAliases | None = None,
) -> ast.expr | None:
    """Find a specific Dagger decorator on a node.

    Args:
        node: The AST node to check.
        decorator_type: One of "object_type", "function", "field", etc.
        aliases: Per-file dagger import aliases. Defaults to the static
            unaliased forms when not provided.

    Returns
    -------
        The decorator AST node, or None if not found.
    """
    if decorator_type not in DAGGER_DECORATOR_NAMES:
        return None

    aliases = aliases or DaggerAliases.default()
    for decorator in node.decorator_list:
        if resolve_dagger_decorator(decorator, aliases) == decorator_type:
            return decorator

    return None


def build_dagger_aliases(  # noqa: C901 — import-shape dispatch
    tree: ast.Module,
) -> DaggerAliases:
    """Compute the dagger alias map for a single source file.

    Walks top-level imports only — local imports inside functions or
    conditional blocks would shadow these names per-scope, but the
    analyzer treats decorators as module-level constructs.
    """
    aliases = DaggerAliases.default()

    for node in ast.iter_child_nodes(tree):
        if isinstance(node, ast.Import):
            for alias in node.names:
                # ``import dagger as d`` → ``d`` resolves to dagger.
                # ``import dagger.mod`` → ``dagger`` is bound (already in
                # defaults); the ``mod`` attribute access is reached via
                # ``dagger.mod`` so we don't need to track it specially.
                if alias.name in ("dagger", "dagger.mod"):
                    aliases.package_aliases.add(alias.asname or "dagger")
            continue

        if not isinstance(node, ast.ImportFrom):
            continue
        if node.level > 0:
            # Relative imports can't bind dagger — skip.
            continue
        if node.module not in ("dagger", "dagger.mod"):
            continue

        for alias in node.names:
            if alias.name == "*":
                continue
            bound = alias.asname or alias.name
            if alias.name in DAGGER_DECORATOR_NAMES:
                aliases.bare_decorators[bound] = alias.name
            if alias.name == "field":
                aliases.bare_field_names.add(bound)
            if alias.name == "mod":
                aliases.package_aliases.add(bound)

    return aliases


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
