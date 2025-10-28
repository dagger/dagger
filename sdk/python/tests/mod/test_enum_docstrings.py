import enum

import pytest

import dagger
from dagger.mod._utils import extract_enum_member_doc


class ExampleEnum(enum.Enum):
    FIRST = "first"
    "This is the first option"

    SECOND = "second"
    "This is the second option"

    THIRD = "third"
    # No docstring for this one


def test_extract_enum_member_doc():
    """Test that we can extract docstrings from enum members using AST parsing."""
    docs = extract_enum_member_doc(ExampleEnum)

    first = docs.get("FIRST")
    assert first is not None
    assert first.description == "This is the first option"
    assert first.deprecated is None

    second = docs.get("SECOND")
    assert second is not None
    assert second.description == "This is the second option"
    assert second.deprecated is None

    assert "THIRD" not in docs  # No docstring for THIRD


class EmptyEnum(enum.Enum):
    VALUE = "value"


def test_extract_enum_member_doc_no_docs():
    """Test that we handle enums with no member docstrings gracefully."""
    docs = extract_enum_member_doc(EmptyEnum)

    assert docs == {}


class DeprecatedExample(enum.Enum):
    ALPHA = "alpha"
    """Alpha value.

    .. deprecated:: 1.2
        Use beta instead.
        Remove no later than 2.0.
    """


def test_extract_enum_member_doc_with_deprecated_directive():
    docs = extract_enum_member_doc(DeprecatedExample)

    meta = docs["ALPHA"]
    assert meta.description == "Alpha value."
    assert meta.deprecated == "1.2\nUse beta instead.\nRemove no later than 2.0."


def test_enum_deprecation():
    with pytest.warns(DeprecationWarning, match="Use 'enum.Enum' instead"):

        class DeprecatedEnum(dagger.Enum):
            FIRST = "first", "This is the first option"
            SECOND = "second", "This is the second option"
