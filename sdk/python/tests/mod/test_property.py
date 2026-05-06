r"""Property-based differential tests using Hypothesis.

The strategies in ``_strategies.py`` generate random Dagger module
sources from a small grammar of supported shapes (base types, Optional
/ list / Annotated wrappers, metadata items). For every generated
module, we run the AST analyzer and the runtime introspector and
assert their outputs match.

Generated combinations cover ground the static fixtures don't —
e.g. ``Optional[Annotated[dagger.Directory, Doc("…"), Name("alt")]]``
with a defaulted parameter alongside a ``list[bool]`` parameter and a
``str`` return type. If a single shape combination breaks the
analyzer, Hypothesis shrinks the failing module to a minimal
repro and prints the source — much faster than guessing fixtures.

To run just this file:

    uv run pytest tests/mod/test_property.py

To run with more examples (the default is small to keep CI fast):

    uv run pytest tests/mod/test_property.py \\
      --hypothesis-profile=thorough

See the ``hypothesis.settings.register_profile`` calls below.
"""

from __future__ import annotations

from hypothesis import given

from dagger.mod._analyzer.analyze import analyze_source_string

from ._differential import assert_metadata_equivalent
from ._runtime_introspect import runtime_introspect
from ._strategies import module_strategy

# Hypothesis profiles ("default" / "thorough") are registered in
# ``conftest.py`` so the ``--hypothesis-profile`` flag works.


@given(source=module_strategy())
def test_property_ast_matches_runtime(source: str) -> None:
    """For every generated module, AST and runtime metadata must match."""
    ast_md = analyze_source_string(source, "Foo")
    runtime_md = runtime_introspect(source, "Foo")
    assert_metadata_equivalent(ast_md, runtime_md)
