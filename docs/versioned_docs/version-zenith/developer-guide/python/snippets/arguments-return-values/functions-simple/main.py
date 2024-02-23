import dagger
from dagger import object_type, function

@object_type
class MyModule:

    @function
    def hello() -> str:
        return "Hello, world"
