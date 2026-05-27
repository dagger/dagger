"""Unit tests for live annotation -> introspection TypeRef mapping."""

from __future__ import annotations

import pytest

import dagger
from dagger.mod._introspect._typeref import type_ref


def test_str_nonoptional_is_nonnull_scalar():
    assert type_ref(str, optional=False) == {
        "kind": "NON_NULL",
        "ofType": {"kind": "SCALAR", "name": "String"},
    }


def test_str_optional_strips_nonnull():
    assert type_ref(str, optional=True) == {"kind": "SCALAR", "name": "String"}


def test_int_scalar_name_is_int():
    assert type_ref(int, optional=True) == {"kind": "SCALAR", "name": "Int"}


def test_bool_and_float():
    assert type_ref(bool, optional=True) == {"kind": "SCALAR", "name": "Boolean"}
    assert type_ref(float, optional=True) == {"kind": "SCALAR", "name": "Float"}


def test_list_of_int_nonoptional():
    assert type_ref(list[int], optional=False) == {
        "kind": "NON_NULL",
        "ofType": {
            "kind": "LIST",
            "ofType": {
                "kind": "NON_NULL",
                "ofType": {"kind": "SCALAR", "name": "Int"},
            },
        },
    }


def test_optional_annotation_implies_nullable():
    # ``str | None`` is nullable regardless of the optional flag.
    assert type_ref(str | None, optional=False) == {"kind": "SCALAR", "name": "String"}


def test_dagger_object_is_object_kind():
    assert type_ref(dagger.Container, optional=True) == {
        "kind": "OBJECT",
        "name": "Container",
    }


def test_dagger_scalar_is_scalar_kind():
    # dagger.Platform subclasses Scalar.
    assert type_ref(dagger.Platform, optional=True) == {
        "kind": "SCALAR",
        "name": "Platform",
    }


def test_union_is_rejected():
    with pytest.raises(TypeError):
        type_ref(int | str, optional=False)
