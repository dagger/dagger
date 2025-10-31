"""
Static (import-free) introspection of user code under the project "src" folder.

This module scans Python source files using the built-in ``ast`` module to find
classes and enums decorated with Dagger decorators, without importing or
executing the user code. It works even if the code cannot be imported due to
missing dependencies.

Scope and goals
- Scan only the project "src" directory (root-level) for Python files.
- Detect:
  - @dagger.object_type classes
  - @dagger.interface classes
  - @dagger.enum_type Enum subclasses
  - @dagger.function methods inside object/interface classes
  - fields declared with dagger.field() in dataclass-like class bodies
- Extract metadata equivalent to what ``Module._objects`` and ``Module._enums``
  provide at runtime, but without loading code:
  - Object/interface name, docstring, fields (names + annotations),
    functions (name, docstring, return annotation, parameters + annotations,
    and optional cache policy).
  - Enum name, docstring, members (name, value as string, and description from
    inline doc comments as string literal after definition when present).

The resulting dataclasses can be used later to build Dagger TypeDefs without
importing the user code. Conversion from annotations (strings) to Dagger type
definitions can be added as a separate step.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from pathlib import Path
import ast
import os
from typing import Any, Dict, Iterable, List, Optional, Tuple

# --------------------------------------------
# Public data model (import-free representation)
# --------------------------------------------

@dataclass
class StaticParam:
    name: str
    annotation: str | None
    has_default: bool = False
    doc: str | None = None


@dataclass
class StaticFunction:
    name: str  # Python function name
    api_name: str | None = None  # Optional API-exposed name override from decorator
    doc: str | None = None  # Python docstring
    doc_override: str | None = None  # Optional description override from decorator
    return_annotation: str | None = None
    parameters: List[StaticParam] = field(default_factory=list)
    cache_policy: str | None = None  # "never" | "session" | <ttl> | None


@dataclass
class StaticField:
    python_name: str  # Attribute name in Python class
    name: str  # API-visible name (after `field(name=...)` override if present)
    annotation: str | None
    init: bool = True  # field(init=...)
    # Extra metadata could be added later (e.g. default path, ignore),
    # but for now we focus on parity with typedef needs.


@dataclass
class StaticObject:
    name: str
    doc: str | None
    interface: bool
    module_path: str = ""  # Python module path where the object is defined (e.g., "pkg.mod")
    fields: Dict[str, StaticField] = field(default_factory=dict)
    functions: Dict[str, StaticFunction] = field(default_factory=dict)


@dataclass
class StaticEnumMember:
    name: str
    value: str
    description: str | None = None


@dataclass
class StaticEnum:
    name: str
    doc: str | None
    module_path: str = ""  # Python module path where the enum is defined
    members: List[StaticEnumMember] = field(default_factory=list)


@dataclass
class StaticScanResult:
    objects: Dict[str, StaticObject] = field(default_factory=dict)
    enums: Dict[str, StaticEnum] = field(default_factory=dict)
    # Mapping from package name (e.g., "pkg" or "pkg.sub") to its docstring from __init__.py
    package_docs: Dict[str, Optional[str]] = field(default_factory=dict)


# --------------------------------------------
# AST utilities
# --------------------------------------------

@dataclass
class _ImportAliases:
    dagger_names: set[str] = field(default_factory=set)  # names that refer to the dagger module e.g., {"dagger", "d"}
    imported_decorators: set[str] = field(default_factory=set)  # names imported from dagger: {"object_type", "function", "interface", "enum_type", "field"}


def _collect_import_aliases(tree: ast.AST) -> _ImportAliases:
    aliases = _ImportAliases()

    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            for alias in node.names:
                if alias.name == "dagger":
                    aliases.dagger_names.add(alias.asname or alias.name)
        elif isinstance(node, ast.ImportFrom):
            if node.module == "dagger":
                for alias in node.names:
                    name = alias.asname or alias.name
                    # We track the specific names we care about.
                    if alias.name in {"object_type", "function", "interface", "enum_type", "field"}:
                        aliases.imported_decorators.add(name)
    return aliases


def _is_name(node: ast.AST, name: str) -> bool:
    return isinstance(node, ast.Name) and node.id == name


def _is_attr_of_any(node: ast.AST, base_names: Iterable[str], attr: str) -> bool:
    return isinstance(node, ast.Attribute) and isinstance(node.value, ast.Name) and node.attr == attr and node.value.id in set(base_names)


def _is_decorator(node: ast.AST, aliases: _ImportAliases, decorator_name: str) -> bool:
    """Return True if the decorator node refers to the given dagger decorator.

    Accepts both @decorator and @module.decorator forms.
    When the decorator is used as a call (@decorator(...)), we receive the Call
    node elsewhere, but here we check the "func" node.
    """
    return (
        _is_name(node, decorator_name) and decorator_name in aliases.imported_decorators
    ) or _is_attr_of_any(node, aliases.dagger_names, decorator_name)


def _decorator_args(node: ast.AST) -> tuple[list[ast.expr], list[ast.keyword]]:
    if isinstance(node, ast.Call):
        return node.args, node.keywords
    return [], []


def _get_kwarg_str(keywords: list[ast.keyword], name: str) -> Optional[str]:
    for kw in keywords:
        if isinstance(kw, ast.keyword) and kw.arg == name:
            return _expr_to_str(kw.value)
    return None


def _get_docstring(node: ast.AST) -> Optional[str]:
    try:
        return ast.get_docstring(node)
    except Exception:
        return None


def _annotation_to_str(node: Optional[ast.AST]) -> Optional[str]:
    if node is None:
        return None
    return _expr_to_str(node)


def _expr_to_str(node: ast.AST) -> str:
    """Best-effort stringification of an annotation/value expression.

    This avoids evaluation/imports and keeps dotted or subscripted forms.
    """
    if isinstance(node, ast.Name):
        return node.id
    if isinstance(node, ast.Attribute) and isinstance(node.value, ast.AST):
        return f"{_expr_to_str(node.value)}.{node.attr}"
    if isinstance(node, ast.Constant):
        return repr(node.value) if not isinstance(node.value, str) else node.value
    if isinstance(node, ast.Subscript):
        value = _expr_to_str(node.value)
        if isinstance(node.slice, ast.Slice):
            sl = ":".join(
                [
                    _expr_to_str(node.slice.lower) if node.slice.lower else "",
                    _expr_to_str(node.slice.upper) if node.slice.upper else "",
                    _expr_to_str(node.slice.step) if node.slice.step else "",
                ]
            )
            return f"{value}[{sl}]"
        # Python 3.8+: slice is an ast.expr directly
        return f"{value}[{_expr_to_str(node.slice)}]"
    if isinstance(node, ast.Tuple):
        return f"({', '.join(_expr_to_str(elt) for elt in node.elts)})"
    if isinstance(node, ast.List):
        return f"[{', '.join(_expr_to_str(elt) for elt in node.elts)}]"
    if isinstance(node, ast.Dict):
        items = [
            f"{_expr_to_str(k)}: {_expr_to_str(v)}" for k, v in zip(node.keys, node.values)
        ]
        return "{" + ", ".join(items) + "}"
    if isinstance(node, ast.BinOp) and isinstance(node.op, ast.BitOr):  # typing | typing
        return f"{_expr_to_str(node.left)} | {_expr_to_str(node.right)}"
    if isinstance(node, ast.UnaryOp) and isinstance(node.op, ast.USub):
        return f"-{_expr_to_str(node.operand)}"
    if isinstance(node, ast.Call):
        fn = _expr_to_str(node.func)
        args = ", ".join(_expr_to_str(a) for a in node.args)
        kwargs = ", ".join(f"{k.arg}={_expr_to_str(k.value)}" for k in node.keywords)
        joined = ", ".join([x for x in [args, kwargs] if x])
        return f"{fn}({joined})"
    # Fallback to AST dump (readable enough, still import-free)
    try:
        return ast.unparse(node)  # type: ignore[attr-defined]
    except Exception:
        return ast.dump(node)


# --------------------------------------------
# Core scanner
# --------------------------------------------

class _FileScanner(ast.NodeVisitor):
    def __init__(self, path: Path, src_dir: Path):
        self.path = path
        self.src_dir = src_dir
        self.aliases = _ImportAliases()
        self.objects: dict[str, StaticObject] = {}
        self.enums: dict[str, StaticEnum] = {}
        # Computed module path for this file, e.g., "pkg.mod" or "pkg.sub" for __init__.py
        rel = self.path.relative_to(self.src_dir)
        if rel.name == "__init__.py":
            # Package module path (e.g., src/foo/__init__.py -> "foo")
            self.module_path = ".".join(rel.parts[:-1])
            self.package_name = self.module_path or None
        else:
            # Regular module path (e.g., src/foo/bar.py -> "foo.bar")
            self.module_path = ".".join(rel.with_suffix("").parts)
            self.package_name = None
        self.package_doc: Optional[str] = None

    def scan(self) -> Tuple[dict[str, StaticObject], dict[str, StaticEnum]]:
        src = self.path.read_text(encoding="utf-8")
        tree = ast.parse(src, filename=str(self.path))
        self.aliases = _collect_import_aliases(tree)
        # If this is a package __init__.py, record its module-level docstring
        if self.package_name is not None:
            try:
                self.package_doc = ast.get_docstring(tree)
            except Exception:
                self.package_doc = None
        self.visit(tree)
        return self.objects, self.enums

    # -- visitors --

    def visit_ClassDef(self, node: ast.ClassDef):
        # Determine if class has dagger decorations
        decos = node.decorator_list
        is_object = False
        is_interface = False
        is_enum_decorated = False
        dec_kwargs: dict[str, list[ast.keyword]] = {}

        for deco in decos:
            target = deco.func if isinstance(deco, ast.Call) else deco
            if _is_decorator(target, self.aliases, "object_type"):
                is_object = True
                _, kws = _decorator_args(deco)
                dec_kwargs["object_type"] = kws
            elif _is_decorator(target, self.aliases, "interface"):
                is_interface = True
                _, kws = _decorator_args(deco)
                dec_kwargs["interface"] = kws
            elif _is_decorator(target, self.aliases, "enum_type"):
                is_enum_decorated = True
                _, kws = _decorator_args(deco)
                dec_kwargs["enum_type"] = kws

        class_doc = _get_docstring(node)

        if is_object or is_interface:
            obj = StaticObject(
                name=node.name,
                doc=class_doc,
                interface=is_interface,
                module_path=self.module_path,
            )
            # Fields: AnnAssign whose value is a call to dagger.field()
            for i, stmt in enumerate(node.body):
                # Field declarations
                if isinstance(stmt, ast.AnnAssign) and isinstance(stmt.target, ast.Name):
                    # match: foo: T = field(...)
                    if isinstance(stmt.value, ast.Call):
                        fn = stmt.value.func
                        if _is_decorator(fn, self.aliases, "field"):
                            field_name = stmt.target.id
                            # Field may have a name override in kwargs
                            _, f_kws = _decorator_args(stmt.value)
                            api_name = _get_kwarg_str(f_kws, "name") or field_name
                            init_kw = _get_kwarg_str(f_kws, "init")
                            init_val = True
                            if init_kw is not None:
                                init_val = init_kw.strip() in {"True", "true", "1"}
                            obj.fields[field_name] = StaticField(
                                python_name=field_name,
                                name=api_name,
                                annotation=_annotation_to_str(stmt.annotation),
                                init=init_val,
                            )
                # Methods decorated with @function
                if isinstance(stmt, (ast.FunctionDef, ast.AsyncFunctionDef)):
                    f_decos = stmt.decorator_list
                    for f_deco in f_decos:
                        target = f_deco.func if isinstance(f_deco, ast.Call) else f_deco
                        if _is_decorator(target, self.aliases, "function"):
                            _, f_kws = _decorator_args(f_deco)
                            cache = _get_kwarg_str(f_kws, "cache")
                            name_override = _get_kwarg_str(f_kws, "name")
                            doc_override = _get_kwarg_str(f_kws, "doc")
                            func = self._build_function(stmt, cache)
                            func.api_name = name_override
                            func.doc_override = doc_override
                            obj.functions[func.name] = func
                            break
            self.objects[obj.name] = obj

        if is_enum_decorated:
            enum_members: list[StaticEnumMember] = []
            for i, stmt in enumerate(node.body):
                # We consider enum members to be simple assignments Name = <expr>
                if isinstance(stmt, ast.Assign) and len(stmt.targets) == 1 and isinstance(stmt.targets[0], ast.Name):
                    value_str = _expr_to_str(stmt.value)
                    member_name = stmt.targets[0].id
                    description = self._get_inline_string_after(node.body, i)
                    enum_members.append(StaticEnumMember(name=member_name, value=value_str, description=description))
            self.enums[node.name] = StaticEnum(
                name=node.name,
                doc=class_doc,
                module_path=self.module_path,
                members=enum_members,
            )

        # Continue generic visiting for nested classes if any
        self.generic_visit(node)

    def _build_function(self, node: ast.FunctionDef | ast.AsyncFunctionDef, cache_policy: Optional[str]) -> StaticFunction:
        params: list[StaticParam] = []
        # positional and keyword-only args (ignore varargs/kwargs for typedefs here)
        arg_nodes: list[ast.arg] = []
        if node.args.args:
            arg_nodes.extend(node.args.args)
        if node.args.kwonlyargs:
            arg_nodes.extend(node.args.kwonlyargs)
        # defaults correspond to last N args in args list
        total_defaults = len(node.args.defaults)
        default_start = len(node.args.args) - total_defaults
        defaults_idx = set(range(max(default_start, 0), len(node.args.args)))

        for idx, a in enumerate(node.args.args):
            if a.arg == "self":
                continue
            params.append(
                StaticParam(
                    name=a.arg,
                    annotation=_annotation_to_str(a.annotation),
                    has_default=(idx in defaults_idx),
                )
            )
        for a in node.args.kwonlyargs:
            params.append(
                StaticParam(
                    name=a.arg,
                    annotation=_annotation_to_str(a.annotation),
                    has_default=(a.default is not None),
                )
            )

        return StaticFunction(
            name=node.name,
            doc=_get_docstring(node),
            return_annotation=_annotation_to_str(node.returns),
            parameters=params,
            cache_policy=cache_policy,
        )

    @staticmethod
    def _get_inline_string_after(class_body: list[ast.stmt], index: int) -> Optional[str]:
        next_idx = index + 1
        if next_idx >= len(class_body):
            return None
        next_stmt = class_body[next_idx]
        if isinstance(next_stmt, ast.Expr) and isinstance(next_stmt.value, ast.Constant) and isinstance(next_stmt.value.value, str):
            return next_stmt.value.value.strip()
        return None


# --------------------------------------------
# Public API
# --------------------------------------------

def scan_src(project_root: os.PathLike[str] | str) -> StaticScanResult:
    """Scan the project's root-level "src" directory and return static metadata.

    Parameters
    ----------
    project_root:
        Path to the project root which contains the "src" directory.

    Returns
    -------
    StaticScanResult
        A static representation of objects and enums defined with dagger decorators.
    """
    root = Path(project_root)
    src_dir = root / "src"
    result = StaticScanResult()

    if not src_dir.exists() or not src_dir.is_dir():
        return result

    for py in sorted(src_dir.rglob("*.py")):
        # Only scan files under root/src. Skip cache/hidden dirs quickly.
        rel = py.relative_to(src_dir)
        if any(part.startswith(".") or part == "__pycache__" for part in rel.parts):
            continue
        try:
            scanner = _FileScanner(py, src_dir)
            objs, enums = scanner.scan()
            # Merge results (last one wins if duplicate names)
            result.objects.update(objs)
            result.enums.update(enums)
            # Merge package doc if this file is a package __init__.py
            if getattr(scanner, "package_name", None):
                result.package_docs[scanner.package_name] = scanner.package_doc
        except SyntaxError:
            # Skip files with syntax errors to keep scanning robust.
            continue
        except OSError:
            continue

    return result


__all__ = [
    "StaticParam",
    "StaticFunction",
    "StaticField",
    "StaticObject",
    "StaticEnumMember",
    "StaticEnum",
    "StaticScanResult",
    "scan_src",
]
