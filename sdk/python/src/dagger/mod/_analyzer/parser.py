"""AST parser for extracting module declarations.

This module parses Python source files and extracts decorated
classes, functions, and fields for Dagger module registration.
"""

from __future__ import annotations

import ast
import json
import logging
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
    DaggerAliases,
    build_dagger_aliases,
    extract_decorator_info,
    find_decorator,
    has_decorator,
    is_classmethod,
    is_staticmethod,
)

logger = logging.getLogger(__name__)


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


def _parse_docstring_deprecated(raw_doc: str) -> tuple[str | None, str | None]:
    """Parse a docstring that may contain a ``.. deprecated::`` directive.

    Returns ``(doc, deprecated)`` with three-state semantics for
    ``deprecated``:

    - ``None`` when the docstring has no ``.. deprecated::`` directive.
    - ``""`` when the directive is present but carries no message.
    - The stripped message otherwise.

    ``doc`` is the non-deprecated portion of the docstring (or None if
    only the deprecation directive was present).
    """
    import re

    match = re.search(r"\.\.\s+deprecated::\s*(.*)", raw_doc, re.IGNORECASE)
    if not match:
        return raw_doc, None

    deprecated_msg = match.group(1).strip()
    # Get the doc portion before the deprecated directive
    doc_part = raw_doc[: match.start()].strip()
    return doc_part or None, deprecated_msg or ""


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

        # Per-file origin of the bare name ``field``; used to disambiguate
        # ``dagger.field`` from ``dataclasses.field`` when called unqualified.
        self._file_field_origin: dict[Path, str | None] = {}

        # Per-file map of dagger import aliases (``import dagger as d``,
        # ``from dagger import object_type as ot``, etc.). Built once per
        # file in ``_build_dagger_aliases``.
        self._dagger_aliases: dict[Path, DaggerAliases] = {}

        # Module-level constants per source file, keyed by (file, name).
        # Initialized here so ``_eval_constant`` can rely on it existing even
        # when called before ``_collect_module_constants`` has run.
        self._module_constants: dict[Path, dict[str, ast.expr]] = {}

        # Module-level type aliases per source file, e.g.
        # ``Source = Annotated[dagger.Directory, dagger.DefaultPath(".")]``.
        # Kept separate from ``_module_constants`` so value-defaults and
        # type-aliases can't bleed into each other's resolution paths.
        self._module_type_aliases: dict[Path, dict[str, ast.expr]] = {}

        # Per-file map of names brought in by relative imports to the parsed
        # file they came from. Populated in ``_collect_relative_imports``
        # and consumed by ``_expand_alias`` (cross-file alias resolution)
        # and ``_eval_constant`` (cross-file constant resolution).
        self._relative_import_origins: dict[Path, dict[str, Path]] = {}

        # File currently being extracted. Set by ``_extract_declarations`` so
        # ``_eval_constant`` can scope name lookups to the containing file.
        self._current_file: Path | None = None

        # Deferred external-constructor declarations, resolved in a second
        # pass after every class has been parsed so the lookup does not
        # depend on file/class iteration order.
        self._pending_external_constructors: list[tuple[str, ast.Assign, Path]] = []

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

        # Phase 1.5: Compute per-file dagger import aliases. Needed before
        # ``_collect_declaration_names`` because the latter detects
        # decorators (``@d.object_type`` from ``import dagger as d``).
        self._build_dagger_aliases()

        # Phase 2: Collect declaration names (for forward references)
        self._collect_declaration_names()

        # Phase 2.5: Collect module-level constants for default resolution
        self._collect_module_constants()

        # Phase 2.6: Map relative-import names to the file that defines them
        # (so cross-file aliases and constants can be resolved).
        self._collect_relative_imports()

        # Record per-file origin of the unqualified ``field`` name.
        self._track_field_origins()

        # Phase 3: Build namespace and resolver
        self._build_namespace()

        # Phase 4: Extract full declarations
        self._extract_declarations()

        # Phase 5: Resolve external constructors once every class is known
        self._resolve_external_constructors()

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

    def _build_dagger_aliases(self) -> None:
        """Compute the dagger import alias map for each parsed file."""
        for file_path, tree in self._asts.items():
            self._dagger_aliases[file_path] = build_dagger_aliases(tree)

    def _aliases_for(self, file_path: Path | None) -> DaggerAliases:
        """Return the dagger alias map for a file (defaults if unknown)."""
        if file_path is None:
            return DaggerAliases.default()
        return self._dagger_aliases.get(file_path) or DaggerAliases.default()

    def _collect_declaration_names(self) -> None:
        """Collect names of decorated top-level classes for forward references.

        Only considers top-level classes: the module's public API surface lives
        at module scope, and ``_extract_declarations`` is also top-level-only.
        Previously this phase used ``ast.walk`` and would register nested
        decorated classes that were never actually emitted as metadata,
        leaving dangling type references.
        """
        for file_path, tree in self._asts.items():
            aliases = self._aliases_for(file_path)
            for node in ast.iter_child_nodes(tree):
                if not isinstance(node, ast.ClassDef):
                    continue
                if has_decorator(node, "object_type", aliases):
                    self._declared_objects.add(node.name)
                elif has_decorator(node, "interface", aliases):
                    self._declared_interfaces.add(node.name)
                elif has_decorator(node, "enum_type", aliases) or self._is_enum_subclass(
                    node
                ):
                    self._declared_enums.add(node.name)

    _ENUM_BASE_NAMES = frozenset(
        {"Enum", "IntEnum", "StrEnum", "Flag", "IntFlag", "ReprEnum"}
    )

    def _is_enum_subclass(self, node: ast.ClassDef) -> bool:
        """Check if a class inherits from a stdlib enum base.

        Covers ``Enum`` as well as ``IntEnum``, ``StrEnum``, ``Flag``,
        ``IntFlag`` and ``ReprEnum`` (all valid Dagger enum shapes),
        whether imported bare or via ``enum.``.
        """
        for base in node.bases:
            if isinstance(base, ast.Attribute) and base.attr in self._ENUM_BASE_NAMES:
                return True
            if isinstance(base, ast.Name) and base.id in self._ENUM_BASE_NAMES:
                return True
        return False

    def _collect_module_constants(self) -> None:
        """Collect module-level constant assignments for default value resolution.

        This allows resolving references like ``FAVES`` when used as
        default values in function signatures. Constants are scoped per
        file so two files defining ``DEFAULT = ...`` with different values
        don't silently overwrite one another.

        Type-shaped assignments (``Source = Annotated[...]``,
        ``Src = dagger.Directory``, ``MaybeDir = dagger.Directory | None``)
        are also recorded into ``_module_type_aliases`` so that names used
        as type annotations can be expanded to the underlying expression.
        """
        for file_path, tree in self._asts.items():
            constants = self._module_constants.setdefault(file_path, {})
            aliases = self._module_type_aliases.setdefault(file_path, {})
            for node in ast.iter_child_nodes(tree):
                if isinstance(node, ast.Assign):
                    for target in node.targets:
                        if isinstance(target, ast.Name):
                            constants[target.id] = node.value
                            if self._looks_like_type_expr(node.value):
                                aliases[target.id] = node.value
                elif (
                    isinstance(node, ast.AnnAssign)
                    and isinstance(node.target, ast.Name)
                    and node.value is not None
                ):
                    constants[node.target.id] = node.value
                    if self._looks_like_type_expr(node.value):
                        aliases[node.target.id] = node.value
                elif (
                    # PEP 695: ``type Source = Annotated[...]``. Distinct AST
                    # node from Assign/AnnAssign — registered explicitly here
                    # so the alias map and the resolver share one path.
                    isinstance(node, ast.TypeAlias)
                    and isinstance(node.name, ast.Name)
                ):
                    aliases[node.name.id] = node.value

    def _collect_relative_imports(self) -> None:
        """Map relative-import names to the parsed file they were defined in.

        For ``from .types import Source`` in ``pkg/main.py``, record
        ``(pkg/main.py, "Source") -> pkg/types.py`` provided ``pkg/types.py``
        is also in the parsed file set. ``_expand_alias`` consults this map
        to follow aliases across files; ``_eval_constant`` can do the same
        for constants.

        The mapping is best-effort — when the target file isn't in the
        parsed set (third-party package, dynamic ``__path__``, etc.), the
        entry is just omitted and resolution falls back to the existing
        warn/stub behavior.
        """
        resolved_paths = {
            file_path: file_path.resolve() for file_path in self._asts
        }
        # Index by resolved path so we can look up ``pkg/types.py`` regardless
        # of whether the user passed it as an absolute or relative path.
        path_index: dict[Path, Path] = {}
        for original, resolved in resolved_paths.items():
            path_index[resolved] = original

        for file_path, tree in self._asts.items():
            mapping = self._relative_import_origins.setdefault(file_path, {})
            current_resolved = resolved_paths[file_path]
            for node in ast.iter_child_nodes(tree):
                if not isinstance(node, ast.ImportFrom) or node.level <= 0:
                    continue
                target = self._resolve_relative_import_target(
                    current_resolved, node.level, node.module, path_index
                )
                if target is None:
                    continue
                for alias in node.names:
                    if alias.name == "*":
                        continue
                    bound = alias.asname or alias.name
                    mapping[bound] = target

    @staticmethod
    def _resolve_relative_import_target(
        current_file: Path,
        level: int,
        module: str | None,
        path_index: dict[Path, Path],
    ) -> Path | None:
        """Return the parsed-file path for ``from <level-dots><module> import …``.

        ``level`` is 1 for ``from . import x``, 2 for ``from .. import x``,
        etc. ``module`` is the dotted suffix (``"types"`` for
        ``from .types import Source``) or ``None`` for ``from . import x``.
        """
        base = current_file.parent
        for _ in range(level - 1):
            base = base.parent
        if module:
            target_dir = base
            for part in module.split("."):
                target_dir = target_dir / part
            for cand in (target_dir.with_suffix(".py"), target_dir / "__init__.py"):
                resolved = cand.resolve() if cand.exists() else cand
                if resolved in path_index:
                    return path_index[resolved]
        # Bare ``from . import x`` — no module to resolve to a file.
        return None

    def _looks_like_type_expr(self, node: ast.expr) -> bool:
        """Return True when ``node`` looks like a type expression.

        We restrict alias expansion to RHS shapes that can plausibly be a
        type — ``Annotated[...]``, generics, ``T | None``, attribute access,
        and bare names — so plain string/number constants stay out of the
        type-resolution path.
        """
        if isinstance(node, (ast.Subscript, ast.Attribute, ast.Name)):
            return True
        return isinstance(node, ast.BinOp) and isinstance(node.op, ast.BitOr)

    def _expand_alias(
        self,
        annotation: ast.expr,
        file_path: Path,
    ) -> ast.expr:
        """Expand a module-level type alias to its underlying expression.

        Walks chained aliases (``B = A``, ``A = dagger.Directory``) and
        protects against cycles by tracking the names already seen. Also
        follows aliases across files — when a name was imported via
        ``from .types import Source``, expansion continues using
        ``types.py``'s alias map so the foreign file's
        ``Source = Annotated[...]`` is honored.

        If the annotation isn't a known alias (or expansion hits a cycle),
        returns the most recently expanded node — which may be the
        original input.
        """
        seen: set[tuple[Path, str]] = set()
        current = annotation
        current_file = file_path
        while isinstance(current, ast.Name):
            name = current.id
            local_aliases = self._module_type_aliases.get(current_file, {})
            if name in local_aliases:
                key = (current_file, name)
                if key in seen:
                    break
                seen.add(key)
                current = local_aliases[name]
                continue
            origin = self._relative_import_origins.get(current_file, {}).get(name)
            if origin is not None:
                origin_aliases = self._module_type_aliases.get(origin, {})
                if name in origin_aliases:
                    key = (origin, name)
                    if key in seen:
                        break
                    seen.add(key)
                    # Switch context: continue expanding inside the origin file
                    # so any further chained aliases use that file's map.
                    current_file = origin
                    current = origin_aliases[name]
                    continue
            break
        return current

    def _build_namespace(self) -> None:
        """Build the namespace for type resolution."""
        # Combine all ASTs for namespace building
        self._namespace = StubNamespace()

        for file_path, tree in self._asts.items():
            self._add_imports_from_tree(tree, file_path)

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

    def _add_imports_from_tree(self, tree: ast.Module, file_path: Path) -> None:
        """Extract imports from an AST tree and add to namespace."""
        assert self._namespace is not None
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                for alias in node.names:
                    self._namespace.add_import(alias.name, alias.asname)
            elif isinstance(node, ast.ImportFrom):
                for alias in node.names:
                    if alias.name == "*":
                        # Static analysis can't expand a star import — we
                        # don't read the foreign module's symbols. Decorated
                        # classes from the same package are still picked up
                        # via cross-file declaration scanning, but module-
                        # level constants and type aliases imported via
                        # ``from .x import *`` will not resolve. Warn so
                        # users see why the schema differs from runtime.
                        prefix = "." * node.level
                        target = f"{prefix}{node.module or ''}" or "."
                        logger.warning(
                            "%s:%d: ``from %s import *`` is not expanded by "
                            "the AST analyzer; type aliases and constants "
                            "from %r will not resolve. Import names "
                            "explicitly to ensure they are picked up.",
                            file_path,
                            getattr(node, "lineno", 0),
                            target,
                            target,
                        )
                        continue
                    if node.level > 0:
                        # Relative imports (``from . import x``,
                        # ``from .pkg import x``) cannot be resolved without
                        # the importing package's context, which static
                        # analysis lacks. Bind the name to a stub so type
                        # annotations don't NameError; cross-file decorated
                        # classes are added separately via add_declared_type.
                        self._namespace.add_relative_import(
                            alias.name, alias.asname, node.module, node.level
                        )
                    else:
                        self._namespace.add_from_import(
                            node.module or "", alias.name, alias.asname
                        )

    _NON_DAGGER_FIELD_MODULES = frozenset({"dataclasses", "attrs", "attr", "pydantic"})

    def _bare_field_is_dagger(self, file_path: Path) -> bool:
        """Return True when bare ``field`` in a file refers to dagger.field.

        Defaults to True (dagger) when no import of ``field`` is found in the
        file: that matches the common pattern of importing ``dagger`` as a
        module and using ``dagger.field(...)`` elsewhere while declaring
        ``field: Something = field()`` inline. Falls back to False only when
        the file explicitly imports ``field`` from a non-dagger module.
        """
        origin = self._file_field_origin.get(file_path)
        if origin is None:
            return True
        top = origin.split(".", 1)[0]
        return top not in self._NON_DAGGER_FIELD_MODULES

    def _track_field_origins(self) -> None:
        """Record which module the unqualified ``field`` name came from.

        A file that does ``from dataclasses import field`` (or from attrs,
        pydantic, etc.) must not treat ``field(...)`` calls as dagger.field.
        """
        for file_path, tree in self._asts.items():
            origin: str | None = None
            for node in ast.walk(tree):
                if not isinstance(node, ast.ImportFrom):
                    continue
                module = node.module or ""
                for alias in node.names:
                    bound = alias.asname or alias.name
                    if bound == "field":
                        origin = module
                        break
                if origin is not None:
                    break
            self._file_field_origin[file_path] = origin

    def _extract_declarations(self) -> None:
        """Extract full declarations from all ASTs."""
        for file_path, tree in self._asts.items():
            self._current_file = file_path
            try:
                for node in ast.iter_child_nodes(tree):
                    if isinstance(node, ast.ClassDef):
                        self._extract_class(node, file_path)
            finally:
                self._current_file = None

    def _extract_class(self, node: ast.ClassDef, file_path: Path) -> None:
        """Extract a class declaration."""
        aliases = self._aliases_for(file_path)
        # Check for @object_type
        if has_decorator(node, "object_type", aliases):
            obj = self._parse_object_type(node, file_path, is_interface=False)
            self._objects[obj.name] = obj
            return

        # Check for @interface
        if has_decorator(node, "interface", aliases):
            obj = self._parse_object_type(node, file_path, is_interface=True)
            self._objects[obj.name] = obj
            return

        # Check for @enum_type or enum.Enum subclass
        if has_decorator(node, "enum_type", aliases) or self._is_enum_subclass(node):
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

        with self._resolver.in_class(node.name):
            # Get decorator info for metadata
            decorator_type = "interface" if is_interface else "object_type"
            aliases = self._aliases_for(file_path)
            decorator = find_decorator(node, decorator_type, aliases)
            decorator_info = extract_decorator_info(decorator) if decorator else None

            # Extract deprecation
            deprecated = None
            if decorator_info and "deprecated" in decorator_info.kwargs:
                deprecated = decorator_info.kwargs["deprecated"]

            # Extract fields, functions, and constructor
            fields, functions, init_params, constructor = self._extract_class_members(
                node, file_path
            )

            # Resolve constructor
            if constructor is None and not is_interface:
                constructor = self._resolve_constructor(node, file_path, init_params)

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

    def _extract_class_members(
        self,
        node: ast.ClassDef,
        file_path: Path,
    ) -> tuple[
        list[FieldMetadata],
        list[FunctionMetadata],
        list[ParameterMetadata],
        FunctionMetadata | None,
    ]:
        """Extract fields, functions, init params, and constructor from a class body."""
        fields: list[FieldMetadata] = []
        functions: list[FunctionMetadata] = []
        init_params: list[ParameterMetadata] = []
        constructor: FunctionMetadata | None = None

        for item in node.body:
            if isinstance(item, ast.AnnAssign) and isinstance(item.target, ast.Name):
                if self._is_non_field_annotation(item.annotation):
                    continue
                self._process_annotated_assign(
                    item, file_path, node.name, fields, init_params
                )
            elif isinstance(item, ast.Assign):
                # Only defer assignments that look like external-constructor
                # declarations. Everything else (plain class-level constants)
                # is ignored at class scope.
                if self._looks_like_external_constructor(item):
                    self._pending_external_constructors.append(
                        (node.name, item, file_path)
                    )
            elif isinstance(item, (ast.FunctionDef, ast.AsyncFunctionDef)):
                aliases = self._aliases_for(file_path)
                if item.name == "create" and is_classmethod(item):
                    constructor = self._parse_constructor(item, file_path, node.name)
                elif item.name == "__init__":
                    constructor = self._parse_init_constructor(
                        item, file_path, node.name
                    )
                elif has_decorator(item, "function", aliases):
                    func = self._parse_function(item, file_path, node.name)
                    functions.append(func)

        seen_names = {f.python_name for f in functions}
        functions.extend(self._find_inherited_functions(node, node.name, seen_names))

        return fields, functions, init_params, constructor

    def _process_annotated_assign(
        self,
        item: ast.AnnAssign,
        file_path: Path,
        class_name: str,
        fields: list[FieldMetadata],
        init_params: list[ParameterMetadata],
    ) -> None:
        """Process an annotated assignment for both field and constructor param."""
        is_initvar = self._is_initvar_annotation(item.annotation)

        if not is_initvar:
            field = self._parse_field(item, file_path, class_name)
            if field is not None:
                fields.append(field)

        param = self._parse_class_assignment_as_param(
            item, file_path, class_name, is_initvar=is_initvar
        )
        if param is not None:
            init_params.append(param)

    def _resolve_constructor(
        self,
        node: ast.ClassDef,
        file_path: Path,
        init_params: list[ParameterMetadata],
    ) -> FunctionMetadata:
        """Resolve the constructor: inherited or default from init params."""
        inherited = self._find_inherited_constructor(node, file_path)
        if inherited is not None:
            return inherited

        return FunctionMetadata(
            python_name="__init__",
            api_name="",
            return_type_annotation=node.name,
            resolved_return_type=ResolvedType(kind="object", name=node.name),
            parameters=init_params,
            doc=get_docstring(node),
            deprecated=None,
            cache_policy=None,
            is_check=False,
            is_async=False,
            is_classmethod=False,
            is_constructor=True,
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
            aliases = self._aliases_for(file_path)
            # Match field(), dagger.field(), mod.field(), and aliased forms
            # like ``from dagger import field as fld; fld()``. The bare
            # ``field`` name is ambiguous (it could be dataclasses.field) and
            # still goes through ``_bare_field_is_dagger``; explicit aliases
            # are trusted unconditionally because the import statement was
            # unambiguous. Attribute access matches when the value is in the
            # dagger package alias set, which excludes dataclasses/attrs/etc.
            is_name_field = isinstance(func, ast.Name) and func.id in (
                aliases.bare_field_names
            )
            if is_name_field and func.id == "field":
                is_name_field = self._bare_field_is_dagger(file_path)
            is_attr_field = (
                isinstance(func, ast.Attribute)
                and func.attr == "field"
                and isinstance(func.value, ast.Name)
                and func.value.id in aliases.package_aliases
            )
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
        # Expand module-level type aliases (``Src = dagger.Directory``,
        # ``Source = Annotated[dagger.Directory, dagger.DefaultPath(".")]``)
        # so both the resolver and the Annotated-metadata extractor see the
        # underlying expression rather than a bare alias name.
        expanded = self._expand_alias(node.annotation, file_path)
        resolved_type = self._resolver.resolve(expanded, location=location)

        # Extract Annotated metadata
        annotated_meta = extract_annotated_metadata(expanded)

        # Get API name
        api_name = field_kwargs.get("name") or normalize_name(python_name)

        # Get default value
        has_default = "default" in field_kwargs or "default_factory" in field_kwargs
        default_value = field_kwargs.get("default")
        if default_value is None and "default_factory" in field_kwargs:
            # We don't invoke the factory; whatever _eval_constant returned
            # becomes the static default. The name_map hard-codes list → []
            # and dict → {}; other factories fall through to None and the
            # parameter is registered as optional-without-default.
            default_value = field_kwargs["default_factory"]

        return FieldMetadata(
            python_name=python_name,
            api_name=api_name,
            type_annotation=get_annotation_string(node.annotation),
            resolved_type=resolved_type,
            has_default=has_default,
            default_value=self._serialize_default(default_value, name=python_name),
            deprecated=field_kwargs.get("deprecated"),
            init=field_kwargs.get("init", True),
            doc=annotated_meta.doc,
            location=location,
        )

    def _is_initvar_annotation(self, annotation: ast.expr) -> bool:
        """Check if annotation is ``dataclasses.InitVar[T]`` or ``InitVar[T]``."""
        if not isinstance(annotation, ast.Subscript):
            return False
        value = annotation.value
        if isinstance(value, ast.Attribute):
            return value.attr == "InitVar"
        return isinstance(value, ast.Name) and value.id == "InitVar"

    def _is_non_field_annotation(self, annotation: ast.expr) -> bool:
        """True for annotations that don't declare a field or init parameter.

        Covers ``ClassVar[...]`` and ``Final[...]`` (plus ``typing.`` prefixed
        forms). These mark class-level constants, not Dagger fields.
        """
        names = {"ClassVar", "Final"}

        def _matches(node: ast.expr) -> bool:
            if isinstance(node, ast.Name):
                return node.id in names
            if isinstance(node, ast.Attribute):
                return node.attr in names
            return False

        if _matches(annotation):
            return True
        return isinstance(annotation, ast.Subscript) and _matches(annotation.value)

    def _unwrap_initvar(self, annotation: ast.expr) -> ast.expr:
        """Unwrap InitVar[T] to get the inner type T."""
        if self._is_initvar_annotation(annotation):
            return annotation.slice  # type: ignore[return-value]
        return annotation

    def _parse_class_assignment_as_param(
        self,
        node: ast.AnnAssign,
        file_path: Path,
        class_name: str,
        *,
        is_initvar: bool = False,
    ) -> ParameterMetadata | None:
        """Parse an annotated class assignment as a potential constructor parameter.

        Handles all class-level annotated assignments including:
        - dagger.field() with init=True (default)
        - dataclasses.InitVar[T] declarations
        - Simple annotated assignments (e.g., name: str = "default")

        Returns None if the assignment has init=False.
        """
        assert self._resolver is not None
        assert isinstance(node.target, ast.Name)

        python_name = node.target.id

        # Get effective annotation (unwrap InitVar if needed)
        annotation = (
            self._unwrap_initvar(node.annotation) if is_initvar else node.annotation
        )
        # Expand module-level type aliases before metadata extraction and
        # type resolution; see ``_expand_alias``.
        annotation = self._expand_alias(annotation, file_path)

        # Determine init eligibility and default value
        has_default = False
        default_value = None

        if node.value is not None:
            if isinstance(node.value, ast.Call):
                # Extract kwargs from any field-like call
                call_kwargs: dict[str, Any] = {}
                for keyword in node.value.keywords:
                    if keyword.arg is not None:
                        call_kwargs[keyword.arg] = self._eval_constant(keyword.value)

                # If init=False, this is not a constructor parameter
                if call_kwargs.get("init") is False:
                    return None

                # Extract default value from the call
                if "default" in call_kwargs:
                    has_default = True
                    default_value = call_kwargs["default"]
                elif "default_factory" in call_kwargs:
                    has_default = True
                    default_value = call_kwargs["default_factory"]
            else:
                # Simple expression as default value
                has_default = True
                default_value = self._eval_constant(node.value)

        # Resolve type
        location = get_location(node, str(file_path))
        resolved_type = self._resolver.resolve(annotation, location=location)

        # Extract Annotated metadata
        annotated_meta = extract_annotated_metadata(annotation)

        # Build parameter
        api_name = normalize_name(python_name)
        if annotated_meta and annotated_meta.name:
            api_name = annotated_meta.name

        return ParameterMetadata(
            python_name=python_name,
            api_name=api_name,
            type_annotation=get_annotation_string(annotation),
            resolved_type=resolved_type,
            is_nullable=resolved_type.is_optional,
            has_default=has_default,
            default_value=(
                self._serialize_default(default_value, name=python_name)
                if has_default
                else None
            ),
            doc=annotated_meta.doc if annotated_meta else None,
            ignore=annotated_meta.ignore if annotated_meta else None,
            default_path=annotated_meta.default_path if annotated_meta else None,
            default_address=annotated_meta.default_address if annotated_meta else None,
            deprecated=annotated_meta.deprecated if annotated_meta else None,
            alt_name=annotated_meta.name if annotated_meta else None,
            location=location,
        )

    def _looks_like_external_constructor(self, node: ast.Assign) -> bool:
        """Cheap pre-check for ``name = function(...)`` shape at class scope."""
        if len(node.targets) != 1 or not isinstance(node.targets[0], ast.Name):
            return False
        return isinstance(node.value, ast.Call)

    def _resolve_external_constructors(self) -> None:
        """Resolve deferred external-constructor declarations.

        Runs after every class has been parsed, so a ``foo = function(Bar)``
        in class A can reference a class Bar declared later in the file or
        in a different source file without depending on iteration order.
        """
        for class_name, assign_node, file_path in self._pending_external_constructors:
            owner = self._objects.get(class_name)
            if owner is None:
                continue
            self._current_file = file_path
            try:
                func = self._parse_external_constructor(
                    assign_node, file_path, class_name
                )
            finally:
                self._current_file = None
            if func is None:
                target_name = self._external_constructor_target(assign_node)
                logger.warning(
                    "External constructor %r in %r references unknown target "
                    "%r; skipping (is the target decorated with @object_type?).",
                    assign_node.targets[0].id
                    if isinstance(assign_node.targets[0], ast.Name)
                    else "?",
                    class_name,
                    target_name or "?",
                )
                continue
            owner.functions.append(func)

    def _external_constructor_target(self, node: ast.Assign) -> str | None:
        """Best-effort extraction of the target class name for logging."""
        if not isinstance(node.value, ast.Call):
            return None
        match = self._match_function_constructor(node.value)
        if match is None:
            return None
        return match[0]

    def _parse_external_constructor(
        self,
        node: ast.Assign,
        file_path: Path,
        class_name: str,
    ) -> FunctionMetadata | None:
        """Parse external constructor pattern: ``name = function(ClassName)``.

        Handles:
        - ``external = function(External)``
        - ``alternative = function(doc="...")(External)``
        """
        if len(node.targets) != 1 or not isinstance(node.targets[0], ast.Name):
            return None

        call_node = node.value
        if not isinstance(call_node, ast.Call):
            return None

        match = self._match_function_constructor(call_node)
        if match is None:
            return None
        target_class_name, func_kwargs = match

        target_obj = self._objects.get(target_class_name)
        if target_obj is None:
            return None

        ctor = target_obj.constructor
        doc = func_kwargs.get("doc")
        if doc is None and ctor:
            doc = ctor.doc
        if doc is None:
            doc = target_obj.doc

        return FunctionMetadata(
            python_name=node.targets[0].id,
            api_name=normalize_name(node.targets[0].id),
            return_type_annotation=target_class_name,
            resolved_return_type=ResolvedType(kind="object", name=target_class_name),
            parameters=list(ctor.parameters) if ctor else [],
            doc=doc,
            deprecated=func_kwargs.get("deprecated"),
            cache_policy=func_kwargs.get("cache"),
            is_check=False,
            is_async=False,
            is_classmethod=False,
            is_constructor=False,
            location=get_location(node, str(file_path)),
        )

    def _match_function_constructor(
        self,
        call_node: ast.Call,
    ) -> tuple[str, dict[str, Any]] | None:
        """Match ``function(Cls)`` or ``function(doc="...")(Cls)`` patterns.

        Returns (target_class_name, kwargs) or None.
        """
        if self._is_function_ref(call_node.func):
            return self._extract_function_call_target(call_node, call_node)
        if (
            isinstance(call_node.func, ast.Call)
            and self._is_function_ref(call_node.func.func)
            and call_node.args
            and isinstance(call_node.args[0], ast.Name)
        ):
            return self._extract_function_call_target(call_node, call_node.func)
        return None

    def _extract_function_call_target(
        self,
        args_node: ast.Call,
        kwargs_node: ast.Call,
    ) -> tuple[str, dict[str, Any]] | None:
        """Extract target class name and kwargs from a function() call."""
        if not args_node.args or not isinstance(args_node.args[0], ast.Name):
            return None
        target = args_node.args[0].id
        kwargs: dict[str, Any] = {}
        for kw in kwargs_node.keywords:
            if kw.arg is not None:
                kwargs[kw.arg] = self._eval_constant(kw.value)
        return target, kwargs

    def _is_function_ref(self, node: ast.expr) -> bool:
        """Check if a node references 'function' or 'dagger.function'."""
        if isinstance(node, ast.Name):
            return node.id == "function"
        if isinstance(node, ast.Attribute):
            return node.attr == "function"
        return False

    def _find_inherited_constructor(
        self,
        node: ast.ClassDef,
        file_path: Path,
    ) -> FunctionMetadata | None:
        """Look for an alternative constructor (create classmethod) in base classes."""
        for base in node.bases:
            base_name = None
            if isinstance(base, ast.Name):
                base_name = base.id
            elif isinstance(base, ast.Attribute):
                base_name = base.attr

            if base_name is None:
                continue

            # Search for the base class definition in all parsed ASTs
            base_class = self._find_class_def(base_name)
            if base_class is None:
                continue

            # Check for create classmethod in the base class
            for item in base_class.body:
                if (
                    isinstance(item, (ast.FunctionDef, ast.AsyncFunctionDef))
                    and item.name == "create"
                    and is_classmethod(item)
                ):
                    return self._parse_constructor(item, file_path, node.name)

        return None

    def _find_class_def(self, class_name: str) -> ast.ClassDef | None:
        """Find a class definition by name in all parsed ASTs."""
        found = self._find_class_def_with_file(class_name)
        return found[0] if found else None

    def _find_class_def_with_file(
        self, class_name: str
    ) -> tuple[ast.ClassDef, Path] | None:
        """Find a class definition (and its source file) in all parsed ASTs."""
        for path, tree in self._asts.items():
            for ast_node in ast.iter_child_nodes(tree):
                if isinstance(ast_node, ast.ClassDef) and ast_node.name == class_name:
                    return ast_node, path
        return None

    def _find_inherited_functions(
        self,
        node: ast.ClassDef,
        class_name: str,
        seen_names: set[str],
        visited: set[str] | None = None,
    ) -> list[FunctionMetadata]:
        """Collect ``@function``-decorated methods from base classes via MRO.

        ``class_name`` is the originating (child) class — used so resolution
        of ``Self`` returns the child type, matching Python's runtime
        behavior. ``seen_names`` accumulates Python names already discovered
        on the child or earlier-walked bases so that overrides win.
        """
        if visited is None:
            visited = {node.name}

        inherited: list[FunctionMetadata] = []
        for base in node.bases:
            base_name = None
            if isinstance(base, ast.Name):
                base_name = base.id
            elif isinstance(base, ast.Attribute):
                base_name = base.attr

            if base_name is None or base_name in visited:
                continue
            visited.add(base_name)

            found = self._find_class_def_with_file(base_name)
            if found is None:
                continue
            base_class, base_file = found

            base_aliases = self._aliases_for(base_file)
            for item in base_class.body:
                if (
                    isinstance(item, (ast.FunctionDef, ast.AsyncFunctionDef))
                    and has_decorator(item, "function", base_aliases)
                    and item.name not in seen_names
                ):
                    func = self._parse_function(item, base_file, class_name)
                    inherited.append(func)
                    seen_names.add(item.name)

            inherited.extend(
                self._find_inherited_functions(
                    base_class, class_name, seen_names, visited
                )
            )
        return inherited

    def _parse_function(
        self,
        node: ast.FunctionDef | ast.AsyncFunctionDef,
        file_path: Path,
        class_name: str,
    ) -> FunctionMetadata:
        """Parse a @function decorated method."""
        assert self._resolver is not None

        # Get decorator info
        aliases = self._aliases_for(file_path)
        decorator = find_decorator(node, "function", aliases)
        decorator_info = extract_decorator_info(decorator) if decorator else None

        # Check for decorator flags
        is_check = has_decorator(node, "check", aliases)
        is_generate = has_decorator(node, "generate", aliases)
        is_service = has_decorator(node, "up", aliases)

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
            expanded_return = self._expand_alias(node.returns, file_path)
            resolved_return = self._resolver.resolve(expanded_return, location=location)
        else:
            return_annotation = "None"
            resolved_return = ResolvedType(kind="void", name="None", is_optional=True)

        # Parse parameters. ``@staticmethod`` has no implicit receiver
        # (no ``self``/``cls``), so the first positional is a regular
        # argument and must not be skipped.
        skip_first = not is_staticmethod(node)
        parameters = self._parse_parameters(node, file_path, skip_first=skip_first)

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
            is_service=is_service,
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
            expanded_return = self._expand_alias(node.returns, file_path)
            resolved_return = self._resolver.resolve(expanded_return, location=location)
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

    def _parse_init_constructor(
        self,
        node: ast.FunctionDef | ast.AsyncFunctionDef,
        file_path: Path,
        class_name: str,
    ) -> FunctionMetadata:
        """Parse an explicit __init__ method as a constructor.

        When __init__ is defined explicitly, its parameters override
        the auto-generated parameters from field declarations.
        """
        location = get_location(node, str(file_path))

        # Parse parameters (skip self)
        parameters = self._parse_parameters(node, file_path, skip_first=True)

        return FunctionMetadata(
            python_name="__init__",
            api_name="",
            return_type_annotation=class_name,
            resolved_return_type=ResolvedType(kind="object", name=class_name),
            parameters=parameters,
            doc=get_docstring(node),
            deprecated=None,
            cache_policy=None,
            is_check=False,
            is_async=False,
            is_classmethod=False,
            is_constructor=True,
            location=location,
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

        # Positional-only come before regular positional; defaults in args.defaults
        # are right-aligned against the combined sequence.
        positional_args = list(args.posonlyargs) + list(args.args)
        all_args = positional_args + list(args.kwonlyargs)

        defaults_offset = len(positional_args) - len(args.defaults)
        kw_defaults = {
            arg.arg: default
            for arg, default in zip(args.kwonlyargs, args.kw_defaults, strict=False)
            if default is not None
        }

        for i, arg in enumerate(all_args):
            # Skip the receiver (first positional) of a method/classmethod.
            # Don't key this on the name — some users write ``this``/``mcs``,
            # and the receiver should never leak into the Dagger API schema.
            if skip_first and i == 0 and positional_args:
                if arg.arg not in ("self", "cls"):
                    logger.warning(
                        "First parameter of method is named %r (expected "
                        "'self' or 'cls'); skipping it as the receiver.",
                        arg.arg,
                    )
                continue

            python_name = arg.arg
            location = get_location(arg, str(file_path))

            # Get annotation
            if arg.annotation:
                annotation = arg.annotation
                annotation_str = get_annotation_string(annotation)
                # Expand module-level type aliases before metadata extraction
                # and resolution. ``annotation_str`` keeps the alias name so
                # the surface representation matches what the user wrote.
                expanded = self._expand_alias(annotation, file_path)

                # Extract Annotated metadata
                annotated_meta = extract_annotated_metadata(expanded)

                # Resolve type
                resolved_type = self._resolver.resolve(expanded, location=location)
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
                    default_value=(
                        self._serialize_default(default_value, name=python_name)
                        if has_default
                        else None
                    ),
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
                        value, inline_doc = self._extract_enum_member_value(
                            item.value, member_name
                        )

                        # Check for docstring following assignment
                        doc = inline_doc
                        deprecated = None
                        next_idx = i + 1
                        if next_idx < len(node.body):
                            next_item = node.body[next_idx]
                            if (
                                isinstance(next_item, ast.Expr)
                                and isinstance(next_item.value, ast.Constant)
                                and isinstance(next_item.value.value, str)
                            ):
                                raw_doc = next_item.value.value.strip()
                                doc, deprecated = _parse_docstring_deprecated(raw_doc)

                        members.append(
                            EnumMemberMetadata(
                                name=member_name,
                                value=value,
                                doc=doc,
                                deprecated=deprecated,
                            )
                        )

        return EnumTypeMetadata(
            name=node.name,
            doc=get_docstring(node),
            members=members,
            location=get_location(node, str(file_path)),
        )

    def _extract_enum_member_value(
        self,
        value_node: ast.expr,
        member_name: str,
    ) -> tuple[str, str | None]:
        """Extract value and optional inline doc from an enum member assignment.

        Handles:
        - Simple string: ``ACTIVE = "ACTIVE value"`` → ("ACTIVE value", None)
        - Tuple (legacy): ``ACTIVE = "here", "doc"`` → ("here", "doc")
        - Other: uses member name as value
        """
        if isinstance(value_node, ast.Constant):
            return str(value_node.value), None

        # Legacy dagger.Enum pattern: MEMBER = "value", "description"
        if isinstance(value_node, ast.Tuple) and len(value_node.elts) >= 1:
            first = value_node.elts[0]
            value = str(first.value) if isinstance(first, ast.Constant) else member_name
            doc = None
            if len(value_node.elts) > 1:
                second = value_node.elts[1]
                if isinstance(second, ast.Constant) and isinstance(second.value, str):
                    doc = second.value
            return value, doc

        return member_name, None

    def _eval_constant(self, node: ast.expr | None) -> Any:  # noqa: PLR0911, C901
        """Evaluate a constant expression to a Python value."""
        if node is None:
            return None

        if isinstance(node, ast.Constant):
            return node.value
        if isinstance(node, ast.Name):
            name_map = {
                "True": True,
                "False": False,
                "None": None,
                # Callable defaults used as default_factory in fields
                "list": [],
                "dict": {},
            }
            if node.id in name_map:
                return name_map[node.id]
            # Try the current file first, then any file that bound this name
            # via a relative import (``from .constants import DEFAULT``).
            current = self._current_file
            if current is not None:
                file_constants = self._module_constants.get(current, {})
                if node.id in file_constants:
                    return self._eval_constant(file_constants[node.id])
                origin = self._relative_import_origins.get(current, {}).get(node.id)
                if origin is not None:
                    origin_constants = self._module_constants.get(origin, {})
                    if node.id in origin_constants:
                        # Switch the current-file scope so any nested name
                        # references inside the constant resolve against the
                        # foreign file's own constants.
                        previous = self._current_file
                        self._current_file = origin
                        try:
                            return self._eval_constant(origin_constants[node.id])
                        finally:
                            self._current_file = previous
            logger.warning(
                "Unresolved name %r used in a constant expression at line %d; "
                "falling back to the literal string.",
                node.id,
                getattr(node, "lineno", 0),
            )
            return node.id
        if isinstance(node, ast.Attribute):
            # Handle enum member references like Status.INACTIVE → "INACTIVE"
            return node.attr
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

    def _serialize_default(self, value: Any, *, name: str = "") -> Any:
        """Serialize a default value for JSON storage.

        Returns None for values that cannot be represented in JSON, with a
        warning so the user sees why the default didn't reach the engine.
        """
        if value is None:
            return None

        try:
            json.dumps(value)
        except (TypeError, ValueError) as exc:
            logger.warning(
                "Default value for %r cannot be serialized to JSON (%s); "
                "the parameter will behave as if no default were set.",
                name or "<unknown>",
                exc,
            )
            return None
        return value
