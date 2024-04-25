from typing import Final

UI_MASK: Final = "dagger.io/ui.mask"
"""Replace parent span (e.g., `exec /runtime`)."""

UI_PASSTHROUGH: Final = "dagger.io/ui.passthrough"
"""Reveal only child spans (e.g., `python runtime execution` parent span)."""

UI_ENCAPSULATE: Final = "dagger.io/ui.encapsulate"
"""Hide children by default (e.g., test case that runs pipelines)."""
