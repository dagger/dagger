# ruff: noqa: ARG001,PLR0913
from typing import TYPE_CHECKING

from docutils import nodes
from docutils.parsers.rst.directives import admonitions

if TYPE_CHECKING:
    from sphinx.application import Sphinx


class Deprecated(admonitions.Warning):
    """
    Deprecation admonition.

    Overrides the default one to look like a warning
    and has no version requirement.
    """

    node_class = nodes.admonition

    def __init__(self, *args, **kwargs) -> None:
        super().__init__(*args, **kwargs)
        self.arguments = ["Deprecated"]
        self.options["classes"] = ["warning"]


def autodoc_skip(app, what: str, name: str, obj, skip, options) -> bool:
    from dagger.client.gen import __all__ as all_gen

    # The only doc that uses the "module" directive is for the Client page.
    # The others use "autoclass" so skip them here to avoid duplicates.
    if what == "module" and name.split(".")[-1] not in all_gen:
        return True

    return skip


def setup(app: "Sphinx"):
    app.connect("autodoc-skip-member", autodoc_skip)
    app.add_directive("deprecated", Deprecated, override=True)

    return {
        "version": "0.1",
        "parallel_read_safe": True,
        "parallel_write_safe": True,
    }
