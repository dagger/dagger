"""Hypothesis strategies that generate Dagger module source code.

A grammar of supported annotation shapes — base types, recursive
wrappers, metadata, defaults, plus module-level features (aliases,
helper classes, inheritance, ``from __future__ import annotations``,
PEP 695 ``type X = …``) — composed into Python source strings that
both the AST analyzer and the runtime introspector should agree on.

The strategies err on the side of *narrow* rather than *clever*:
nothing in the grammar should produce code that either side rejects,
because the property-based test asserts the two paths agree. Patterns
the analyzer explicitly rejects (Literal, dict, TypeVar, …) are not
generated. Patterns we know diverge (function-call defaults) are also
not generated — they live as ``xfail`` fixtures in
``test_differential.py``.
"""

from __future__ import annotations

import dataclasses
import keyword
import sys

from hypothesis import strategies as st

# ---------------------------------------------------------------------------
# Base types — render to source string + sample default value (if any).
# ---------------------------------------------------------------------------


@dataclasses.dataclass(frozen=True)
class _BaseType:
    annotation: str  # e.g. "str", "dagger.Directory"
    default_strategy: st.SearchStrategy[str] | None  # None = no defaults
    accepts_default_path: bool = False
    accepts_ignore: bool = False


_PRIMITIVE_BASES = [
    _BaseType(
        "str",
        st.one_of(
            st.text(min_size=0, max_size=8, alphabet="abcXYZ_-").map(repr),
            st.just('""'),
        ),
    ),
    _BaseType(
        "int",
        st.integers(min_value=-100, max_value=100).map(str),
    ),
    _BaseType(
        "float",
        st.one_of(st.just("0.0"), st.just("1.5"), st.just("-2.25")),
    ),
    _BaseType("bool", st.sampled_from(["True", "False"])),
    # ``bytes`` is supported as a parameter type but has no JSON-serialisable
    # default form — the AST analyzer warns and drops the default while the
    # runtime keeps the raw value. Generate ``bytes`` parameters without
    # defaults to keep the property test on the agreed-equivalence path.
    _BaseType("bytes", None),
]

_DAGGER_OBJECT_BASES = [
    _BaseType("dagger.Directory", None, accepts_default_path=True, accepts_ignore=True),
    _BaseType("dagger.Container", None),
    _BaseType("dagger.File", None),
    _BaseType("dagger.Secret", None),
    _BaseType("dagger.Service", None),
    _BaseType("dagger.CacheVolume", None),
]

_DAGGER_SCALAR_BASES = [
    _BaseType("dagger.JSON", None),
    _BaseType("dagger.Platform", None),
]

ALL_BASES = _PRIMITIVE_BASES + _DAGGER_OBJECT_BASES + _DAGGER_SCALAR_BASES
base_type_strategy = st.sampled_from(ALL_BASES)


# ---------------------------------------------------------------------------
# Annotated metadata items.
# ---------------------------------------------------------------------------


def _doc_meta() -> st.SearchStrategy[str]:
    return st.text(min_size=1, max_size=20, alphabet="abcdefghijk ").map(
        lambda s: f"Doc({s!r})"
    )


def _name_meta() -> st.SearchStrategy[str]:
    alphabet = "abcdefghijkl"
    return st.text(min_size=1, max_size=8, alphabet=alphabet).map(
        lambda s: f"Name({s!r})"
    )


def _default_path_meta() -> st.SearchStrategy[str]:
    return st.sampled_from(['DefaultPath(".")', 'DefaultPath("./src")'])


def _ignore_meta() -> st.SearchStrategy[str]:
    return st.sampled_from(['Ignore(["**/.git"])', 'Ignore(["node_modules"])'])


def _deprecated_meta() -> st.SearchStrategy[str]:
    return st.just('Deprecated("legacy")')


# ---------------------------------------------------------------------------
# Type expression — recursive wrapping up to a configurable depth.
# ---------------------------------------------------------------------------


@dataclasses.dataclass(frozen=True)
class GeneratedType:
    """A generated annotation source plus the metadata needed to default it."""

    source: str  # the source string to drop into a parameter annotation
    base: _BaseType  # the innermost base type
    is_optional: bool  # whether the outer wrapping accepts None
    is_list: bool  # whether the outer wrapping is a list
    has_annotated_outer: bool  # whether Annotated[…] is at the top level


_FORWARD_REF_NAMES = {"Foo"}  # the always-defined class in render_module


@st.composite
def _annotated_meta_list(  # type: ignore[no-untyped-def]
    draw, base: _BaseType, max_items: int = 2
) -> str:
    """Generate one or more metadata expressions for ``Annotated[…]``."""
    options = [_doc_meta(), _name_meta()]
    if base.accepts_default_path:
        options.append(_default_path_meta())
    if base.accepts_ignore:
        options.append(_ignore_meta())
    if draw(st.booleans()):
        options.append(_deprecated_meta())
    metas = draw(st.lists(st.one_of(*options), min_size=1, max_size=max_items))
    return ", ".join(metas)


@st.composite
def _type_expr(  # type: ignore[no-untyped-def]  # noqa: PLR0911 — wrapper dispatch
    draw,
    *,
    depth: int = 0,
    max_depth: int = 3,
    allow_forward_ref: bool = True,
) -> GeneratedType:
    """Generate a (possibly wrapped) type expression.

    The wrappers Hypothesis can pick: ``Optional[T]``, ``T | None``,
    ``list[T]``, ``Annotated[T, …]``, plus a leaf base type or quoted
    forward reference. Recursion is bounded by ``max_depth`` so the
    generator always terminates in finite time.
    """
    # Leaf — pick a base type, optionally as a forward reference.
    if depth >= max_depth:
        if (
            allow_forward_ref
            and depth > 0  # don't wrap the very top level in a string
            and draw(st.booleans())
        ):
            name = draw(st.sampled_from(sorted(_FORWARD_REF_NAMES)))
            base = _BaseType(name, None)
            return GeneratedType(
                source=f'"{name}"',
                base=base,
                is_optional=False,
                is_list=False,
                has_annotated_outer=False,
            )
        base = draw(base_type_strategy)
        return GeneratedType(
            source=base.annotation,
            base=base,
            is_optional=False,
            is_list=False,
            has_annotated_outer=False,
        )

    # Recursive case — pick a wrapper.
    wrapper = draw(
        st.sampled_from(["base", "optional", "pipe-none", "list", "annotated"])
    )

    if wrapper == "base":
        base = draw(base_type_strategy)
        return GeneratedType(
            source=base.annotation,
            base=base,
            is_optional=False,
            is_list=False,
            has_annotated_outer=False,
        )

    if wrapper == "optional":
        inner = draw(
            _type_expr(
                depth=depth + 1,
                max_depth=max_depth,
                allow_forward_ref=allow_forward_ref,
            )
        )
        return GeneratedType(
            source=f"Optional[{inner.source}]",
            base=inner.base,
            is_optional=True,
            is_list=inner.is_list,
            has_annotated_outer=False,
        )

    if wrapper == "pipe-none":
        # ``"Foo" | None`` is invalid even under ``from __future__``
        # annotations because typing won't accept a bare string literal
        # as an operand of ``|``. Force the inner to be a real type.
        inner = draw(
            _type_expr(
                depth=depth + 1,
                max_depth=max_depth,
                allow_forward_ref=False,
            )
        )
        return GeneratedType(
            source=f"{inner.source} | None",
            base=inner.base,
            is_optional=True,
            is_list=inner.is_list,
            has_annotated_outer=False,
        )

    if wrapper == "list":
        inner = draw(
            _type_expr(
                depth=depth + 1,
                max_depth=max_depth,
                allow_forward_ref=allow_forward_ref,
            )
        )
        return GeneratedType(
            source=f"list[{inner.source}]",
            base=inner.base,
            is_optional=False,
            is_list=True,
            has_annotated_outer=False,
        )

    # Annotated wrapper — last branch.
    inner = draw(
        _type_expr(
            depth=depth + 1,
            max_depth=max_depth,
            allow_forward_ref=allow_forward_ref,
        )
    )
    metas = draw(_annotated_meta_list(inner.base))
    return GeneratedType(
        source=f"Annotated[{inner.source}, {metas}]",
        base=inner.base,
        is_optional=inner.is_optional,
        is_list=inner.is_list,
        has_annotated_outer=True,
    )


# ---------------------------------------------------------------------------
# Parameters and functions.
# ---------------------------------------------------------------------------


@dataclasses.dataclass(frozen=True)
class GeneratedParam:
    name: str
    annotation: str
    default_expr: str | None


_PARAM_NAME_ALPHABET = "abcdefghijklmnopqr"
_RESERVED = {"self", "cls", "this", "msg"}


def param_name_strategy() -> st.SearchStrategy[str]:
    return st.text(min_size=1, max_size=6, alphabet=_PARAM_NAME_ALPHABET).filter(
        lambda s: not keyword.iskeyword(s) and s not in _RESERVED
    )


@st.composite
def param_strategy(  # type: ignore[no-untyped-def]
    draw, *, max_depth: int = 3, allow_forward_ref: bool = True
) -> GeneratedParam:
    """Generate a single function parameter."""
    type_expr = draw(
        _type_expr(max_depth=max_depth, allow_forward_ref=allow_forward_ref)
    )

    # Default expression — must be compatible with the (innermost) base.
    default_expr: str | None = None
    if type_expr.is_optional and draw(st.booleans()):
        default_expr = "None"
    elif type_expr.is_list:
        # No literal default for list parameters (default_factory only works
        # at field declaration scope, not on plain function parameters).
        default_expr = None
    elif (
        not type_expr.is_optional
        and type_expr.base.default_strategy is not None
        and draw(st.booleans())
    ):
        default_expr = draw(type_expr.base.default_strategy)

    return GeneratedParam(
        name=draw(param_name_strategy()),
        annotation=type_expr.source,
        default_expr=default_expr,
    )


@dataclasses.dataclass(frozen=True)
class GeneratedFunction:
    name: str
    params: tuple[GeneratedParam, ...]
    return_annotation: str
    body: str


def _function_name_strategy() -> st.SearchStrategy[str]:
    return st.text(min_size=1, max_size=8, alphabet="abcdefghijk_").filter(
        lambda s: (
            not keyword.iskeyword(s)
            and s not in _RESERVED
            and not s.startswith("_")
            and not s.endswith("_")
        )
    )


@st.composite
def function_strategy(  # type: ignore[no-untyped-def]
    draw, *, allow_forward_ref: bool = True
) -> GeneratedFunction:
    name = draw(_function_name_strategy())
    param_count = draw(st.integers(min_value=0, max_value=3))
    params: list[GeneratedParam] = []
    used_names: set[str] = set()
    attempts = 0
    while len(params) < param_count and attempts < 20:
        p = draw(param_strategy(allow_forward_ref=allow_forward_ref))
        if p.name not in used_names:
            params.append(p)
            used_names.add(p.name)
        attempts += 1
    # Required params must come before defaulted ones (Python rule).
    params.sort(key=lambda p: p.default_expr is not None)

    return_type = draw(_type_expr(max_depth=2, allow_forward_ref=allow_forward_ref))
    # Avoid bytes return — rendering ``return b""`` everywhere is awkward.
    if return_type.base.annotation == "bytes":
        return_type = GeneratedType(
            source="str",
            base=ALL_BASES[0],
            is_optional=False,
            is_list=False,
            has_annotated_outer=False,
        )

    return GeneratedFunction(
        name=name,
        params=tuple(params),
        return_annotation=return_type.source,
        body="        ...",
    )


# ---------------------------------------------------------------------------
# Module-level features.
# ---------------------------------------------------------------------------


@dataclasses.dataclass(frozen=True)
class GeneratedModule:
    """Everything needed to render a complete module source string."""

    use_future_annotations: bool
    aliases: tuple[tuple[str, str], ...]  # (alias_name, alias_target_source)
    pep695_aliases: tuple[tuple[str, str], ...]  # 3.12+ ``type X = …``
    has_helper_class: bool
    foo_inherits_helper: bool
    functions: tuple[GeneratedFunction, ...]


def _alias_name_strategy(prefix: str = "Alias") -> st.SearchStrategy[str]:
    return st.text(min_size=1, max_size=4, alphabet="ABCDEFGH").map(
        lambda s: f"{prefix}{s}"
    )


@st.composite
def _alias_target(draw) -> str:  # type: ignore[no-untyped-def]
    """Generate a type expression suitable as the RHS of an alias."""
    expr = draw(_type_expr(max_depth=2, allow_forward_ref=False))
    return expr.source


@st.composite
def module_strategy(  # type: ignore[no-untyped-def]
    draw, *, max_functions: int = 4
) -> str:
    """Strategy that produces a complete Dagger module source string."""
    use_future_annotations = draw(st.booleans())
    # Forward references like ``"Foo"`` work inside ``Optional[…]``,
    # ``list[…]``, ``Annotated[…]`` regardless, but ``"Foo" | None`` is
    # only valid under ``from __future__ import annotations`` because
    # otherwise the string is evaluated as a value at definition time.
    # Gate forward-ref generation on the future-annotations toggle to
    # keep the property test on legal-Python territory.
    allow_forward_ref = use_future_annotations

    # Module-level type aliases — ``Source = Annotated[…]``.
    alias_count = draw(st.integers(min_value=0, max_value=2))
    aliases: list[tuple[str, str]] = []
    used_alias_names: set[str] = set()
    while len(aliases) < alias_count:
        name = draw(_alias_name_strategy())
        if name in used_alias_names:
            continue
        used_alias_names.add(name)
        aliases.append((name, draw(_alias_target())))

    # PEP 695 ``type X = …`` aliases — only when the runtime can parse them.
    pep695_aliases: list[tuple[str, str]] = []
    if sys.version_info >= (3, 12):
        pep695_count = draw(st.integers(min_value=0, max_value=2))
        while len(pep695_aliases) < pep695_count:
            name = draw(_alias_name_strategy(prefix="PType"))
            if name in used_alias_names:
                continue
            used_alias_names.add(name)
            pep695_aliases.append((name, draw(_alias_target())))

    has_helper_class = draw(st.booleans())
    foo_inherits_helper = has_helper_class and draw(st.booleans())

    n = draw(st.integers(min_value=1, max_value=max_functions))
    fns: list[GeneratedFunction] = []
    used_fn_names: set[str] = set()
    attempts = 0
    while len(fns) < n and attempts < 30:
        f = draw(function_strategy(allow_forward_ref=allow_forward_ref))
        if f.name not in used_fn_names:
            fns.append(f)
            used_fn_names.add(f.name)
        attempts += 1

    return _render_module(
        GeneratedModule(
            use_future_annotations=use_future_annotations,
            aliases=tuple(aliases),
            pep695_aliases=tuple(pep695_aliases),
            has_helper_class=has_helper_class,
            foo_inherits_helper=foo_inherits_helper,
            functions=tuple(fns),
        )
    )


# ---------------------------------------------------------------------------
# Rendering.
# ---------------------------------------------------------------------------


def _render_module(mod: GeneratedModule) -> str:  # noqa: C901 — straight rendering
    lines: list[str] = []
    if mod.use_future_annotations:
        lines.append("from __future__ import annotations")
        lines.append("")
    lines.extend(
        [
            "import dagger",
            "from typing import Annotated, Optional",
            "from dagger import DefaultPath, Deprecated, Doc, Ignore, Name",
            "",
        ]
    )
    for name, target in mod.aliases:
        lines.append(f"{name} = {target}")
    if mod.aliases:
        lines.append("")
    for name, target in mod.pep695_aliases:
        lines.append(f"type {name} = {target}")
    if mod.pep695_aliases:
        lines.append("")

    if mod.has_helper_class:
        lines.extend(
            [
                "@dagger.object_type",
                "class Helper:",
                '    name: str = dagger.field(default="h")',
                "",
                "    @dagger.function",
                "    def hello(self) -> str:",
                "        return self.name",
                "",
            ]
        )

    lines.append("@dagger.object_type")
    base_clause = "(Helper)" if mod.foo_inherits_helper else ""
    lines.append(f"class Foo{base_clause}:")

    if not mod.functions:
        lines.append("    pass")
        return "\n".join(lines) + "\n"

    for fn in mod.functions:
        param_strs = []
        for p in fn.params:
            if p.default_expr is None:
                param_strs.append(f"{p.name}: {p.annotation}")
            else:
                param_strs.append(f"{p.name}: {p.annotation} = {p.default_expr}")
        params_rendered = ", ".join(["self", *param_strs])
        lines.extend(
            [
                "    @dagger.function",
                f"    def {fn.name}({params_rendered}) -> {fn.return_annotation}:",
                fn.body,
                "",
            ]
        )
    return "\n".join(lines) + "\n"
