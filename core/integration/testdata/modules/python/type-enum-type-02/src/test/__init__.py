import dagger
from dagger import function, object_type

@object_type
class Test:
    @function
    def from_proto(self, proto: dagger.NetworkProtocol) -> str:
        return str(proto)

    @function
    def from_proto_default(self, proto: dagger.NetworkProtocol = dagger.NetworkProtocol.UDP) -> str:
        return str(proto)

    @function
    def to_proto(self, proto: str) -> dagger.NetworkProtocol:
        # Doing "dagger.NetworkProtocol(proto)" will fail in Python, so mock
        # it to force sending the invalid value back to the server.
        from dagger.client.base import Enum

        class MockEnum(Enum):
            TCP = "TCP"
            INVALID = "INVALID"

        return MockEnum(proto)
