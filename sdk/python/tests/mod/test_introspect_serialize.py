"""Unit tests for live module -> schematool ModuleTypes JSON."""

from __future__ import annotations

import enum
import logging

from dagger.mod import Module
from dagger.mod._introspect.serialize import live_to_introspection_json


def _types_by_name(resp: dict) -> dict[str, dict]:
    return {t["name"]: t for t in resp["__schema"]["types"]}


def _field(typ: dict, name: str) -> dict:
    return next(f for f in typ["fields"] if f["name"] == name)


def _arg(field: dict, name: str) -> dict:
    return next(a for a in field["args"] if a["name"] == name)


def test_object_function_and_query_constructor():
    mod = Module()

    @mod.object_type
    class Test:
        @mod.function
        def echo(self, msg: str) -> str:
            return msg

    resp = live_to_introspection_json(mod, main_object_name="Test", module_name="test")
    types = _types_by_name(resp)

    assert types["Test"]["kind"] == "OBJECT"
    echo = _field(types["Test"], "echo")
    assert echo["type"] == {
        "kind": "NON_NULL",
        "ofType": {"kind": "SCALAR", "name": "String"},
    }
    assert _arg(echo, "msg")["type"] == {
        "kind": "NON_NULL",
        "ofType": {"kind": "SCALAR", "name": "String"},
    }

    # Query carries the module constructor field.
    assert resp["__schema"]["queryType"]["name"] == "Query"
    ctor = _field(types["Query"], "test")
    assert ctor["type"] == {
        "kind": "NON_NULL",
        "ofType": {"kind": "OBJECT", "name": "Test"},
    }


def test_multiword_names_stay_native_not_camelcased():
    """Function/field/arg names must match the runtime invoke keying.

    The def phase feeds these names to ``dag.function()`` / ``with_arg()``; the
    engine round-trips the registered name back to invoke, where lookup is keyed
    by ``normalize_name(original_name)`` (snake_case). Camel-casing here would
    advertise ``atLevel`` while invoke is keyed by ``at_level`` -> the function
    becomes uncallable (the #13234 fix's regression guard).
    """
    mod = Module()

    @mod.object_type
    class Test:
        @mod.function
        def at_level(self, lvl_arg: int = 5) -> int:
            return lvl_arg

    resp = live_to_introspection_json(mod, main_object_name="Test", module_name="test")
    test = _types_by_name(resp)["Test"]

    fn = _field(test, "at_level")  # not "atLevel"
    assert _arg(fn, "lvl_arg")["defaultValue"] == "5"  # not "lvlArg"


def test_runtime_resolved_default_is_value_not_name():
    """#13234: a ``logging.INFO`` default reaches the schema as ``20``."""
    mod = Module()

    @mod.object_type
    class Test:
        @mod.function
        def at_level(self, level: int = logging.INFO) -> int:
            return level

    resp = live_to_introspection_json(mod, main_object_name="Test", module_name="test")
    level = _arg(_field(_types_by_name(resp)["Test"], "at_level"), "level")
    assert level["defaultValue"] == "20"
    # Optional arg => bare scalar, no NON_NULL wrapper.
    assert level["type"] == {"kind": "SCALAR", "name": "Int"}


def test_enum_emitted_with_members():
    mod = Module()

    @mod.enum_type
    class Lang(enum.Enum):
        GO = "go"
        PYTHON = "python"

    @mod.object_type
    class Test:
        @mod.function
        def noop(self) -> str:
            return ""

    resp = live_to_introspection_json(mod, main_object_name="Test", module_name="test")
    lang = _types_by_name(resp)["Lang"]
    assert lang["kind"] == "ENUM"
    assert {v["name"] for v in lang["enumValues"]} == {"GO", "PYTHON"}
