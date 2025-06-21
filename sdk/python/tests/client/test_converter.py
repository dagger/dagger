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
