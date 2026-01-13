"""Tests for the tracer module."""

import pytest

from pytest_otel.tracer import TestContextManager


class TestParseNodeid:
    """Tests for nodeid parsing."""

    def test_module_only(self):
        """Test parsing a module-only nodeid."""
        ctx = TestContextManager()
        module, cls, func = ctx._parse_nodeid("tests/test_foo.py")

        assert module == "tests/test_foo.py"
        assert cls is None
        assert func is None

    def test_module_function(self):
        """Test parsing a module::function nodeid."""
        ctx = TestContextManager()
        module, cls, func = ctx._parse_nodeid("tests/test_foo.py::test_bar")

        assert module == "tests/test_foo.py"
        assert cls is None
        assert func == "test_bar"

    def test_module_class_function(self):
        """Test parsing a module::class::function nodeid."""
        ctx = TestContextManager()
        module, cls, func = ctx._parse_nodeid("tests/test_foo.py::TestClass::test_method")

        assert module == "tests/test_foo.py"
        assert cls == "TestClass"
        assert func == "test_method"

    def test_parametrized_test(self):
        """Test parsing a parametrized test nodeid."""
        ctx = TestContextManager()
        module, cls, func = ctx._parse_nodeid("tests/test_foo.py::test_bar[param1-param2]")

        assert module == "tests/test_foo.py"
        assert cls is None
        assert func == "test_bar[param1-param2]"

    def test_nested_class(self):
        """Test parsing a nested class nodeid."""
        ctx = TestContextManager()
        module, cls, func = ctx._parse_nodeid(
            "tests/test_foo.py::TestOuter::TestInner::test_method"
        )

        assert module == "tests/test_foo.py"
        assert cls == "TestOuter"
        assert func == "TestInner::test_method"


class TestAttributeNames:
    """Tests for attribute name constants."""

    def test_attribute_names_for_dagger_ui(self):
        """Verify attribute names for Dagger UI integration."""
        from pytest_otel import tracer

        assert tracer.ATTR_UI_BOUNDARY == "dagger.io/ui.boundary"
        assert tracer.ATTR_UI_REVEAL == "dagger.io/ui.reveal"
