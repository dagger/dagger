# Configuration file for the Sphinx documentation builder.
#
# For the full list of built-in configuration values, see the documentation:
# https://www.sphinx-doc.org/en/master/usage/configuration.html

import os
import sys

sys.path.append(os.path.abspath("./_ext"))

project = "Dagger Python SDK"
copyright = "2023, Dagger"  # noqa: A001
author = "Dagger"

extensions = [
    "sphinx.ext.autodoc",
    "sphinx.ext.viewcode",
    "sphinx.ext.napoleon",
    "sphinx.ext.intersphinx",
    "dagger_ext",
]

language = "en"
locale_dirs = []

exclude_patterns = ["_build", "Thumbs.db", ".DS_Store"]
pygments_style = "sphinx"

html_theme = "sphinx_rtd_theme"

intersphinx_mapping = {"python": ("https://docs.python.org/3", None)}
