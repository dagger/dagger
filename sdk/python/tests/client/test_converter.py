import pytest
from cattrs.preconf.json import JsonConverter, make_converter

from dagger.client import base
from dagger.client._core import configure_converter_enum


@pytest.fixture
def conv() -> JsonConverter:
    _conv = make_converter()
    configure_converter_enum(_conv)
    return _conv


class CustomEnum(base.Enum):
    ONE = "1"
    TWO = "2"
    THREE = "3"


def test_enum_structure(conv: JsonConverter):
    assert conv.structure("ONE", CustomEnum) == CustomEnum.ONE


def test_enum_unstructure(conv: JsonConverter):
    assert conv.unstructure(CustomEnum.TWO) == "TWO"


class Policy(base.ValueEnum):
    """Named by convention, sent by the API's value."""

    DEFAULT = "Default"
    PER_SESSION = "PerSession"
    Default = "Default"
    PerSession = "PerSession"


def test_value_enum_aliases():
    assert Policy.PerSession is Policy.PER_SESSION
    assert [m.name for m in Policy] == ["DEFAULT", "PER_SESSION"]


@pytest.mark.parametrize("member", [Policy.PER_SESSION, Policy.PerSession])
def test_value_enum_unstructure(conv: JsonConverter, member: Policy):
    assert conv.unstructure(member) == "PerSession"


@pytest.mark.parametrize("name", ["PER_SESSION", "PerSession"])
def test_value_enum_structure(conv: JsonConverter, name: str):
    assert conv.structure(name, Policy) is Policy.PER_SESSION
