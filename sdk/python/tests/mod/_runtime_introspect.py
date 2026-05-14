"""Runtime introspection helper used for differential testing.

The AST analyzer's correctness invariant is "what `typing.get_type_hints`
plus `inspect` would have computed at runtime." This module imports a
test source as a real module and produces ``ModuleMetadata`` from
runtime objects, so the AST output can be diffed against the runtime
output as ground truth.

This is *test infrastructure* — it imports user code, so it can never
be on the production analyzer path (the whole reason for the AST
refactor in #11803). But for tests, it's the closest thing we have to
an oracle.
"""

from __future__ import annotations

import contextlib
import dataclasses
import enum as enum_module
import importlib.util
import inspect
import sys
import tempfile
import typing
from pathlib import Path
from typing import Any, get_args, get_origin

import typing_extensions

from dagger.mod._analyzer.metadata import (
    EnumMemberMetadata,
    EnumTypeMetadata,
    FieldMetadata,
    FunctionMetadata,
    ModuleMetadata,
    ObjectTypeMetadata,
    ParameterMetadata,
    ResolvedType,
)
from dagger.mod._module import FIELD_DEF_KEY, FUNCTION_DEF_KEY

_DAGGER_OBJECT_TYPES = {
    "Container",
    "Directory",
    "File",
    "Secret",
    "Service",
    "CacheVolume",
    "Socket",
    "ModuleSource",
    "Module",
    "GitRepository",
    "GitRef",
    "Terminal",
    "Host",
    "Client",
}
_DAGGER_SCALAR_TYPES = {
    "Platform",
    "JSON",
    "ContainerID",
    "DirectoryID",
    "FileID",
    "SecretID",
    "ServiceID",
    "CacheVolumeID",
    "SocketID",
    "ModuleSourceID",
    "ModuleID",
    "GitRepositoryID",
    "GitRefID",
    "TerminalID",
}
_PRIMITIVES = {str: "str", int: "int", float: "float", bool: "bool", bytes: "bytes"}


def runtime_introspect(source: str, main_object_name: str) -> ModuleMetadata:
    """Import ``source`` as a fresh module and produce ModuleMetadata.

    A unique module name is used per call so repeated invocations don't
    share state in ``sys.modules``.
    """
    with tempfile.NamedTemporaryFile(
        mode="w", suffix=".py", delete=False, encoding="utf-8"
    ) as f:
        f.write(source)
        tmp_path = Path(f.name)

    mod_name = f"_runtime_introspect_{tmp_path.stem}"
    spec = importlib.util.spec_from_file_location(mod_name, tmp_path)
    assert spec is not None
    assert spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    sys.modules[mod_name] = module
    try:
        spec.loader.exec_module(module)
        return _build_module_metadata(module, main_object_name)
    finally:
        sys.modules.pop(mod_name, None)
        tmp_path.unlink(missing_ok=True)


def runtime_introspect_package(
    files: dict[str, str],
    main_object_name: str,
    *,
    package_name: str | None = None,
) -> ModuleMetadata:
    """Import a multi-file package and produce ModuleMetadata.

    ``files`` is keyed by the path *inside* the package (e.g.
    ``"__init__.py"``, ``"helpers.py"``, ``"sub/__init__.py"``,
    ``"sub/types.py"``). Each value is the source string. The package
    is materialised in a temporary directory, added to ``sys.path``,
    imported as a fresh top-level package, then the package and any
    submodule containing classes that match ``main_object_name`` /
    other decorated classes contribute to ``ModuleMetadata``.

    This mirrors how ``dagger develop`` discovers a real Dagger Python
    module — a package on disk with ``__init__.py`` plus possibly
    submodules — so cross-file relative imports, cross-file aliases,
    and cross-file constants are exercised the way users hit them.
    """
    import shutil
    import uuid

    # Derive a unique top-level package name so repeat calls can't
    # collide in ``sys.modules`` or interfere with the dagger-mod
    # registry across tests.
    if package_name is None:
        package_name = f"_runtime_pkg_{uuid.uuid4().hex[:12]}"

    if "__init__.py" not in files:
        # Always need a top-level __init__.py so the directory is a package.
        files = {"__init__.py": "", **files}

    tmp_root = Path(tempfile.mkdtemp(prefix="_runtime_pkg_"))
    pkg_dir = tmp_root / package_name
    pkg_dir.mkdir()
    try:
        for rel_path, content in files.items():
            target = pkg_dir / rel_path
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(content, encoding="utf-8")

        sys.path.insert(0, str(tmp_root))
        try:
            package = importlib.import_module(package_name)
            return _build_package_metadata(package, main_object_name)
        finally:
            # Drop every cached module under our package so a follow-up
            # call starts clean even if it reuses a name.
            for mod_name in list(sys.modules):
                if mod_name == package_name or mod_name.startswith(package_name + "."):
                    sys.modules.pop(mod_name, None)
            with contextlib.suppress(ValueError):
                sys.path.remove(str(tmp_root))
    finally:
        shutil.rmtree(tmp_root, ignore_errors=True)


def _build_package_metadata(  # noqa: C901 — submodule walking dispatch
    package: Any, main_object_name: str
) -> ModuleMetadata:
    """Walk a package's submodules and merge their decorated classes.

    Submodules are discovered via ``pkgutil.walk_packages`` rather than
    importing everything from the ``__init__`` namespace — that way we
    pick up classes a user split across files even if the ``__init__``
    didn't re-export them (the AST analyzer also walks files directly,
    so this matches its discovery model).
    """
    import pkgutil

    objects: dict[str, ObjectTypeMetadata] = {}
    enums: dict[str, EnumTypeMetadata] = {}

    visited: set[str] = set()

    def _ingest(module: Any) -> None:
        if module.__name__ in visited:
            return
        visited.add(module.__name__)
        for obj in vars(module).values():
            if not isinstance(obj, type):
                continue
            # Only count classes whose ``__module__`` is somewhere inside
            # this package — re-exports from typing/dagger/etc. are skipped.
            if not getattr(obj, "__module__", "").startswith(package.__name__):
                continue
            if hasattr(obj, "__dagger_object_type__"):
                objects.setdefault(obj.__name__, _build_object_metadata(obj))
            elif issubclass(obj, enum_module.Enum) and obj is not enum_module.Enum:
                enums.setdefault(obj.__name__, _build_enum_metadata(obj))

    _ingest(package)
    if hasattr(package, "__path__"):
        for info in pkgutil.walk_packages(
            package.__path__, prefix=package.__name__ + "."
        ):
            try:
                submodule = importlib.import_module(info.name)
            except Exception:  # noqa: BLE001 — third-party submodules may fail
                continue
            _ingest(submodule)

    return ModuleMetadata(
        module_name=main_object_name,
        main_object=main_object_name,
        objects=objects,
        enums=enums,
    )


def _build_module_metadata(module: Any, main_object_name: str) -> ModuleMetadata:
    objects: dict[str, ObjectTypeMetadata] = {}
    enums: dict[str, EnumTypeMetadata] = {}

    for name, obj in vars(module).items():
        if not isinstance(obj, type):
            continue
        if obj.__module__ != module.__name__:
            # Skip re-exported classes from other modules (typing, dagger, …).
            continue
        if hasattr(obj, "__dagger_object_type__"):
            objects[name] = _build_object_metadata(obj)
        elif issubclass(obj, enum_module.Enum) and obj is not enum_module.Enum:
            enums[name] = _build_enum_metadata(obj)

    return ModuleMetadata(
        module_name=main_object_name,
        main_object=main_object_name,
        objects=objects,
        enums=enums,
    )


def _build_object_metadata(cls: type) -> ObjectTypeMetadata:  # noqa: C901
    obj_def = cls.__dagger_object_type__
    is_interface = bool(getattr(obj_def, "interface", False))

    fields: list[FieldMetadata] = []
    if not is_interface and dataclasses.is_dataclass(cls):
        type_hints = typing.get_type_hints(cls, include_extras=True)
        for field in dataclasses.fields(cls):
            field_def = field.metadata.get(FIELD_DEF_KEY)
            if field_def is None:
                continue
            annotation = type_hints.get(field.name, field.type)
            resolved = _resolve_type(annotation, cls.__name__)
            api_name = field_def.name or _normalize_name(field.name)
            has_default = field.default is not dataclasses.MISSING or (
                field.default_factory is not dataclasses.MISSING
            )
            if field.default is not dataclasses.MISSING:
                default_value = field.default
            elif field.default_factory is not dataclasses.MISSING:
                # ``dagger.field(default=list)`` is rewritten to
                # ``default_factory=list`` by the dagger decorator.
                # Call the factory so we compare against the produced
                # value (``[]`` / ``{}`` / …), matching the AST analyzer's
                # name_map which special-cases ``list``/``dict``.
                try:
                    default_value = field.default_factory()
                except Exception:  # noqa: BLE001 — user factory may raise
                    default_value = None
            else:
                default_value = None
            fields.append(
                FieldMetadata(
                    python_name=field.name,
                    api_name=api_name,
                    type_annotation=_annotation_string(annotation),
                    resolved_type=resolved,
                    has_default=has_default,
                    default_value=default_value,
                    deprecated=field_def.deprecated,
                    init=field.init,
                )
            )

    functions: list[FunctionMetadata] = []
    seen: set[str] = set()
    for name, member in inspect.getmembers(cls):
        if name in seen:
            continue
        if not callable(member):
            continue
        meta = getattr(member, FUNCTION_DEF_KEY, None)
        if meta is None:
            continue
        functions.append(_build_function_metadata(cls, member, meta))
        seen.add(name)

    return ObjectTypeMetadata(
        name=cls.__name__,
        is_interface=is_interface,
        doc=inspect.getdoc(cls),
        fields=fields,
        functions=functions,
    )


def _build_function_metadata(owner: type, func: Any, meta: Any) -> FunctionMetadata:
    sig = inspect.signature(func)
    try:
        type_hints = typing.get_type_hints(func, include_extras=True)
    except Exception:  # noqa: BLE001 — get_type_hints raises NameError etc.
        type_hints = {}

    return_annotation = type_hints.get("return", sig.return_annotation)
    if return_annotation is inspect.Signature.empty or return_annotation is None:
        resolved_return = ResolvedType(kind="void", name="None", is_optional=True)
        return_str = "None"
    else:
        resolved_return = _resolve_type(return_annotation, owner.__name__)
        return_str = _annotation_string(return_annotation)

    parameters: list[ParameterMetadata] = []
    for i, (param_name, param) in enumerate(sig.parameters.items()):
        # Skip self/cls receiver. ``inspect.signature`` on a classmethod
        # already strips ``cls``; static methods have no implicit
        # receiver. So only skip the first positional named ``self``.
        if i == 0 and param_name in ("self", "cls"):
            continue
        if param.kind in (
            inspect.Parameter.VAR_POSITIONAL,
            inspect.Parameter.VAR_KEYWORD,
        ):
            continue

        annotation = type_hints.get(param_name, param.annotation)
        if annotation is inspect.Parameter.empty:
            resolved = ResolvedType(kind="primitive", name="Any")
            annotation_str = "Any"
            doc = None
            default_path = None
            default_addr = None
            ignore = None
            deprecated = None
            alt_name = None
        else:
            resolved = _resolve_type(annotation, owner.__name__)
            annotation_str = _annotation_string(annotation)
            doc, default_path, default_addr, ignore, deprecated, alt_name = (
                _extract_annotated_metadata(annotation)
            )

        has_default = param.default is not inspect.Parameter.empty
        default_value = param.default if has_default else None
        api_name = alt_name or _normalize_name(param_name)

        parameters.append(
            ParameterMetadata(
                python_name=param_name,
                api_name=api_name,
                type_annotation=annotation_str,
                resolved_type=resolved,
                is_nullable=resolved.is_optional,
                has_default=has_default,
                default_value=default_value,
                doc=doc,
                ignore=ignore,
                default_path=default_path,
                default_address=default_addr,
                deprecated=deprecated,
                alt_name=alt_name,
            )
        )

    return FunctionMetadata(
        python_name=func.__name__,
        api_name=meta.name or _normalize_name(func.__name__),
        return_type_annotation=return_str,
        resolved_return_type=resolved_return,
        parameters=parameters,
        doc=meta.doc or inspect.getdoc(func),
        deprecated=meta.deprecated,
        cache_policy=meta.cache,
        is_check=meta.check,
        is_generate=meta.generator,
        is_service=meta.service,
        is_async=inspect.iscoroutinefunction(func),
        is_classmethod=isinstance(
            inspect.getattr_static(owner, func.__name__, None), classmethod
        ),
    )


def _build_enum_metadata(cls: type) -> EnumTypeMetadata:
    members: list[EnumMemberMetadata] = []
    for member in cls.__members__.values():
        value = member.value
        members.append(
            EnumMemberMetadata(
                name=member.name,
                value=str(value),
                doc=inspect.getdoc(member) if hasattr(member, "__doc__") else None,
            )
        )
    return EnumTypeMetadata(
        name=cls.__name__,
        doc=inspect.getdoc(cls),
        members=members,
    )


def _resolve_type(  # noqa: C901, PLR0911, PLR0912 — typing dispatch is naturally branchy
    annotation: Any, current_class: str
) -> ResolvedType:
    """Map a runtime type to a ResolvedType matching the AST analyzer's shape."""
    # Unwrap PEP 695 ``type X = …``. ``typing.get_type_hints`` returns the
    # alias object itself; reach the underlying type via ``__value__``.
    if (
        hasattr(annotation, "__value__")
        and type(annotation).__name__ == "TypeAliasType"
    ):
        return _resolve_type(annotation.__value__, current_class)
    # Unwrap Annotated.
    origin = get_origin(annotation)
    if origin is typing.Annotated:
        args = get_args(annotation)
        if args:
            return _resolve_type(args[0], current_class)

    if annotation is None or annotation is type(None):
        return ResolvedType(kind="void", name="None")

    if annotation is typing_extensions.Self:
        return ResolvedType(kind="object", name=current_class, is_self=True)

    # Optional / Union
    if origin is typing.Union:
        args = [a for a in get_args(annotation) if a is not type(None)]
        has_none = any(a is type(None) for a in get_args(annotation))
        if not args:
            return ResolvedType(kind="void", name="None")
        if len(args) == 1:
            inner = _resolve_type(args[0], current_class)
            inner.is_optional = has_none
            return inner
        msg = f"Unsupported union: {annotation!r}"
        raise AssertionError(msg)

    # X | None (PEP 604)
    import types as types_module

    if isinstance(annotation, types_module.UnionType):
        args = [a for a in annotation.__args__ if a is not type(None)]
        has_none = any(a is type(None) for a in annotation.__args__)
        if not args:
            return ResolvedType(kind="void", name="None")
        if len(args) == 1:
            inner = _resolve_type(args[0], current_class)
            inner.is_optional = has_none
            return inner
        msg = f"Unsupported union: {annotation!r}"
        raise AssertionError(msg)

    # list[T] / Sequence[T] etc.
    import collections.abc as collections_abc

    if origin in (list, collections_abc.Sequence, collections_abc.Iterable):
        args = get_args(annotation)
        if args:
            element = _resolve_type(args[0], current_class)
        else:
            element = ResolvedType(kind="primitive", name="Any")
        return ResolvedType(kind="list", name="list", element_type=element)

    if origin is tuple:
        args = get_args(annotation)
        non_ellipsis = [a for a in args if a is not Ellipsis]
        element = (
            _resolve_type(non_ellipsis[0], current_class)
            if non_ellipsis
            else ResolvedType(kind="primitive", name="Any")
        )
        return ResolvedType(kind="list", name="list", element_type=element)

    if isinstance(annotation, type):
        if annotation in _PRIMITIVES:
            return ResolvedType(kind="primitive", name=_PRIMITIVES[annotation])

        name = annotation.__name__
        if name in _DAGGER_OBJECT_TYPES:
            return ResolvedType(kind="object", name=name)
        if name in _DAGGER_SCALAR_TYPES:
            return ResolvedType(kind="scalar", name=name)
        if issubclass(annotation, enum_module.Enum):
            return ResolvedType(kind="enum", name=name)
        # Best effort: treat unknown user types as objects.
        return ResolvedType(kind="object", name=name)

    # Fallback for things we don't fully model — caller should compare
    # only to AST output that's also a fallback object.
    return ResolvedType(kind="object", name=str(annotation))


_AnnotatedMetaTuple = tuple[
    str | None,  # doc
    str | None,  # default_path
    str | None,  # default_address
    list[str] | None,  # ignore
    str | None,  # deprecated
    str | None,  # alt_name
]


def _extract_annotated_metadata(  # noqa: C901, PLR0912 — typing dispatch
    annotation: Any,
) -> _AnnotatedMetaTuple:
    """Return (doc, default_path, default_address, ignore, deprecated, alt_name)."""
    if (
        hasattr(annotation, "__value__")
        and type(annotation).__name__ == "TypeAliasType"
    ):
        return _extract_annotated_metadata(annotation.__value__)
    if get_origin(annotation) is not typing.Annotated:
        # Could still be wrapped: Optional[Annotated[T, …]] etc. Drill in.
        if get_origin(annotation) is typing.Union:
            for arg in get_args(annotation):
                if arg is type(None):
                    continue
                inner = _extract_annotated_metadata(arg)
                if any(inner):
                    return inner
        import types as types_module

        if isinstance(annotation, types_module.UnionType):
            for arg in annotation.__args__:
                if arg is type(None):
                    continue
                inner = _extract_annotated_metadata(arg)
                if any(inner):
                    return inner
        return (None, None, None, None, None, None)

    metadata = annotation.__metadata__
    doc = default_path = default_addr = deprecated = alt_name = None
    ignore: list[str] | None = None
    for entry in metadata:
        cls_name = type(entry).__name__
        if cls_name == "Doc":
            doc = getattr(entry, "documentation", None)
        elif cls_name == "Name":
            alt_name = getattr(entry, "name", None)
        elif cls_name == "DefaultPath":
            default_path = getattr(entry, "from_context", None)
        elif cls_name == "DefaultAddress":
            default_addr = getattr(entry, "from_context", None)
        elif cls_name == "Ignore":
            ignore = list(getattr(entry, "patterns", []) or []) or None
        elif cls_name == "Deprecated":
            deprecated = getattr(entry, "reason", "") or ""
    return (doc, default_path, default_addr, ignore, deprecated, alt_name)


def _annotation_string(annotation: Any) -> str:
    """Mimic ``ast.unparse`` for runtime type objects.

    Only used for the ``type_annotation`` string field, which is for
    display rather than type identity. The differential comparator
    ignores it.
    """
    return repr(annotation)


def _normalize_name(name: str) -> str:
    if (
        name.endswith("_")
        and len(name) > 1
        and name[-2] != "_"
        and not name.startswith("_")
    ):
        return name[:-1]
    return name
