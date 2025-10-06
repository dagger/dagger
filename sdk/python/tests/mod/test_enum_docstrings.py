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

    assert docs.get("FIRST") == "This is the first option"
    assert docs.get("SECOND") == "This is the second option"
    assert "THIRD" not in docs  # No docstring for THIRD


class EmptyEnum(enum.Enum):
    VALUE = "value"


def test_extract_enum_member_doc_no_docs():
    """Test that we handle enums with no member docstrings gracefully."""
    docs = extract_enum_member_doc(EmptyEnum)

    assert docs == {}


def test_enum_deprecation():
    with pytest.warns(DeprecationWarning, match="Use 'enum.Enum' instead"):

        class DeprecatedEnum(dagger.Enum):
            FIRST = "first", "This is the first option"
            SECOND = "second", "This is the second option"
