"""Test module, short description

Long description, with full sentences.
"""

from dagger import field, object_type


@object_type
class Test:
    """Test object, short description"""

    foo: str = field(default="foo")
