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


def setup(app: "Sphinx"):
    app.add_directive("deprecated", Deprecated, override=True)

    return {
        "version": "0.1",
        "parallel_read_safe": True,
        "parallel_write_safe": True,
    }
