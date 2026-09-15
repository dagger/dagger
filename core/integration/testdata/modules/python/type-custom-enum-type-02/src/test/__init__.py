import enum

import dagger

@dagger.enum_type
class Status(enum.Enum):
    """Enum for Status"""

    ACTIVE = "ACTIVE value"
    """Active status"""

    INACTIVE = "INACTIVE value"
    """Inactive status"""

    WEIRD = "WEIRD"
    """Weird status"""


@dagger.object_type
class Test:
    status: Status = dagger.field(default=Status.INACTIVE)

    @dagger.function
    def from_status(self, status: Status) -> str:
        return status.value

    @dagger.function
    def from_status_opt(self, status: Status | None) -> str:
        return status.value if status else ""

    @dagger.function
    def to_status(self, status: str) -> Status:
        # Doing "Status(status)" will fail in Python, so mock
        # it to force sending the invalid value back to the server.
        class MockEnum(enum.Enum):
            ACTIVE = "ACTIVE value"
            INACTIVE = "INACTIVE value"
            INVALID = "INVALID"

        return MockEnum(status)
