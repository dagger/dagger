"""Tests for annotations with `from __future__ import annotations`.

This file MUST have `from __future__ import annotations` at the top to test
the issue where annotations don't work when they are stringified.
See: https://github.com/dagger/dagger/issues/11554
"""

from __future__ import annotations

from typing import Annotated

from typing_extensions import Doc

import dagger
from dagger import DefaultPath, Deprecated, Ignore, Name
from dagger.mod import Module


def test_default_path_with_future_annotations():
    """Test that DefaultPath works with `from __future__ import annotations`."""
    mod = Module()

    @mod.object_type
    class Foo:
        @mod.function
        def build(
            self,
            src: Annotated[dagger.Directory, DefaultPath(".")],
        ) -> str:
            return "ok"

    fn = mod.get_object("Foo").functions["build"]
    param = fn.parameters["src"]

    assert param.default_path == "."
    assert param.is_optional is True


def test_doc_with_future_annotations():
    """Test that Doc works with `from __future__ import annotations`."""
    mod = Module()

    @mod.object_type
    class Foo:
        @mod.function
        def build(
            self,
            src: Annotated[str, Doc("Source directory")],
        ) -> str:
            return "ok"

    fn = mod.get_object("Foo").functions["build"]
    param = fn.parameters["src"]

    assert param.doc == "Source directory"


def test_name_with_future_annotations():
    """Test that Name works with `from __future__ import annotations`."""
    mod = Module()

    @mod.object_type
    class Foo:
        @mod.function
        def build(
            self,
            src: Annotated[str, Name("source")],
        ) -> str:
            return "ok"

    fn = mod.get_object("Foo").functions["build"]
    param = fn.parameters["src"]

    assert param.name == "source"


def test_ignore_with_future_annotations():
    """Test that Ignore works with `from __future__ import annotations`."""
    mod = Module()

    @mod.object_type
    class Foo:
        @mod.function
        def build(
            self,
            src: Annotated[dagger.Directory, Ignore(["*.tmp", ".git"])],
        ) -> str:
            return "ok"

    fn = mod.get_object("Foo").functions["build"]
    param = fn.parameters["src"]

    assert param.ignore == ["*.tmp", ".git"]


def test_deprecated_with_future_annotations():
    """Test that Deprecated works with `from __future__ import annotations`."""
    mod = Module()

    @mod.object_type
    class Foo:
        @mod.function
        def build(
            self,
            src: Annotated[str, Deprecated("Use new_src instead")] = "",
        ) -> str:
            return "ok"

    fn = mod.get_object("Foo").functions["build"]
    param = fn.parameters["src"]

    assert param.deprecated == "Use new_src instead"
