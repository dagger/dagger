"""Test module, short description

Long description, with full sentences.
"""

from dagger import function, object_type

from .foo import Foo


@object_type
class Test:
    """Test object, short description"""

    foo = function(Foo)
