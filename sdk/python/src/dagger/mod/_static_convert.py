from __future__ import annotations

import re
from dataclasses import dataclass
from typing import Optional, Tuple

import dagger
from dagger import dag

from ._static_scan import StaticScanResult


@dataclass
class TypeDefResult:
    td: "dagger.TypeDef"
    is_optional: bool = False


_BUILTINS = {
    "str": dagger.TypeDefKind.STRING_KIND,
    "int": dagger.TypeDefKind.INTEGER_KIND,
    "float": dagger.TypeDefKind.FLOAT_KIND,
    "bool": dagger.TypeDefKind.BOOLEAN_KIND,
    "None": dagger.TypeDefKind.VOID_KIND,
    "none": dagger.TypeDefKind.VOID_KIND,
}

_LIST_PREFIXES = ("list[", "List[", "typing.List[")
_OPTIONAL_PREFIXES = ("Optional[", "typing.Optional[")


def _strip_outer(text: str, prefix: str, suffix: str) -> str:
    return text[len(prefix) : -len(suffix)]


def _normalize(s: Optional[str]) -> Optional[str]:
    if s is None:
        return None
    return "".join(c for c in s.strip() if c not in "\n\r\t")


def _split_union_none(s: str) -> tuple[str, bool]:
    # Detect patterns like "T | None" or "None | T"
    parts = [p.strip() for p in s.split("|")]
    if len(parts) == 2 and (parts[0].lower() == "none" or parts[1].lower() == "none"):
        other = parts[1] if parts[0].lower() == "none" else parts[0]
        return other, True
    return s, False


def typedef_from_str(annotation: Optional[str], scan: StaticScanResult) -> TypeDefResult:
    """Convert a string annotation to a dagger.TypeDef.

    Supports a minimal subset sufficient for module typedefs:
    - builtins: str, int, float, bool, None
    - Optional[T] / typing.Optional[T] / T | None
    - list[T] / List[T] / typing.List[T]
    - Named types: if present in scan.objects or scan.enums, map accordingly; otherwise
      treat as object using final dotted segment (e.g., dagger.Container -> Container).
    """
    text = _normalize(annotation)
    td = dag.type_def()
    if not text:
        # Unknown -> assume void (None)
        return TypeDefResult(td.with_kind(dagger.TypeDefKind.VOID_KIND), False)

    # Optional[...] wrapper
    opt = False
    for pref in _OPTIONAL_PREFIXES:
        if text.startswith(pref) and text.endswith("]"):
            inner = _strip_outer(text, pref, "]")
            res = typedef_from_str(inner, scan)
            res.is_optional = True
            return res

    # X | None
    text, opt = _split_union_none(text)

    # list[...] wrapper
    for pref in _LIST_PREFIXES:
        if text.startswith(pref) and text.endswith("]"):
            inner = _strip_outer(text, pref, "]")
            res = typedef_from_str(inner, scan)
            td = dag.type_def().with_list_of(res.td)
            if res.is_optional:
                td = td.with_optional(True)
            return TypeDefResult(td, opt)

    # Builtins
    if text in _BUILTINS:
        td = td.with_kind(_BUILTINS[text])
        if opt:
            td = td.with_optional(True)
        return TypeDefResult(td, opt)

    # Dotted name -> last segment
    name = text.split(".")[-1]

    # Enums from scan
    if name in scan.enums:
        td = td.with_enum(name, description=scan.enums[name].doc)
        if opt:
            td = td.with_optional(True)
        return TypeDefResult(td, opt)

    # Objects/interfaces from scan
    if name in scan.objects:
        obj = scan.objects[name]
        td = td.with_interface(name) if obj.interface else td.with_object(name)
        if opt:
            td = td.with_optional(True)
        return TypeDefResult(td, opt)

    # Default: treat as object type by name
    td = td.with_object(name)
    if opt:
        td = td.with_optional(True)
    return TypeDefResult(td, opt)


def _split_top_level_args(s: str) -> list[str]:
    args: list[str] = []
    buf: list[str] = []
    depth = 0
    for ch in s:
        if ch in "([{":
            depth += 1
        elif ch in ")]}":
            depth -= 1
        elif ch == "," and depth == 0:
            args.append("".join(buf).strip())
            buf = []
            continue
        buf.append(ch)
    if buf:
        args.append("".join(buf).strip())
    return args


_ANNOTATED_PREFIXES = ("Annotated[", "typing.Annotated[", "typing_extensions.Annotated[")


def _unwrap_wrappers(text: str) -> str:
    # Remove Optional[...] and list[...] wrappers repeatedly, and unwrap X | None
    changed = True
    while changed and text:
        changed = False
        # Optional[...] forms
        for pref in _OPTIONAL_PREFIXES:
            if text.startswith(pref) and text.endswith("]"):
                text = _strip_outer(text, pref, "]").strip()
                changed = True
        # X | None union
        inner, opt = _split_union_none(text)
        if opt:
            text = inner.strip()
            changed = True
        # list[...] forms
        for pref in _LIST_PREFIXES:
            if text.startswith(pref) and text.endswith("]"):
                text = _strip_outer(text, pref, "]").strip()
                changed = True
    return text


def doc_from_str(annotation: Optional[str], scan: StaticScanResult) -> Optional[str]:
    """Best-effort static description for a type annotation string.

    Mirrors runtime `get_doc(field.return_type)` to the extent possible without imports:
    - If an Annotated[..., Doc("...")] is present, prefer that documentation.
    - Otherwise, if the base named type matches a scanned object/interface/enum,
      return that type's docstring captured by the scanner; if missing, fall back to the
      parent package's docstring collected from `__init__.py`.
    - Builtins or unknown names return None.

    Also unwraps Optional[...] / T | None and list[...] to reach the base element type.
    """
    def _parent_package(module_path: str) -> Optional[str]:
        return module_path.rsplit(".", 1)[0] if module_path and "." in module_path else None

    text = _normalize(annotation)
    if not text:
        return None

    # Extract Annotated[...] base and Doc(...) if present
    doc_override: Optional[str] = None
    for pref in _ANNOTATED_PREFIXES:
        if text.startswith(pref) and text.endswith("]"):
            inner = _strip_outer(text, pref, "]")
            # Look for Doc("...") anywhere in the inner meta
            m = re.search(r"Doc\((['\"])\s*(.*?)\s*\1\)", inner)
            if m:
                doc_override = m.group(2)
            # Base is the first top-level argument
            parts = _split_top_level_args(inner)
            if parts:
                text = parts[0]
            break

    # Unwrap wrappers to get base element type
    base = _unwrap_wrappers(text)
    if not base:
        return doc_override

    # Use last dotted segment as type name
    name = base.split(".")[-1]

    # Prefer explicit Doc(...) if available
    if doc_override:
        return doc_override

    # Otherwise look up scanned types, with fallback to package docs
    if name in scan.objects:
        obj = scan.objects[name]
        if obj.doc:
            return obj.doc
        pkg = _parent_package(obj.module_path)
        return scan.package_docs.get(pkg) if pkg else None
    if name in scan.enums:
        en = scan.enums[name]
        if en.doc:
            return en.doc
        pkg = _parent_package(en.module_path)
        return scan.package_docs.get(pkg) if pkg else None
    return None


__all__ = ["TypeDefResult", "typedef_from_str", "doc_from_str"]
