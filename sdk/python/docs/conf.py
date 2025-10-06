# Configuration file for the Sphinx documentation builder.
#
# For the full list of built-in configuration values, see the documentation:
# https://www.sphinx-doc.org/en/master/usage/configuration.html

import pathlib
import sys

sys.path.append(str(pathlib.Path("./_ext").resolve()))

project = "Dagger Python SDK"
copyright = "2022, Dagger"  # noqa: A001
author = "Dagger"

extensions = [
    "sphinx.ext.napoleon",
    "sphinx.ext.autodoc",
    "sphinx.ext.autodoc.typehints",
    "sphinx.ext.viewcode",
    "sphinx.ext.intersphinx",
    "dagger_ext",
]

language = "en"
locale_dirs = []

exclude_patterns = ["_build", "Thumbs.db", ".DS_Store"]
pygments_style = "sphinx"
autodoc_default_options = {"members": True, "show-inheritance": True}
autodoc_mock_imports = ["_typeshed"]

html_theme = "sphinx_rtd_theme"

intersphinx_mapping = {"python": ("https://docs.python.org/3/", None)}
