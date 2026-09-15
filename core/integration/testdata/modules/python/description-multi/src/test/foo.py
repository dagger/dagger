"""Not the main file"""

from dagger import field, object_type


@object_type
class Foo:
    bar: str = field(default="bar")
