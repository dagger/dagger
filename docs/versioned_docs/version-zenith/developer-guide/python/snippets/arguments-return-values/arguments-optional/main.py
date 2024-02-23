import dagger
from dagger import object_type, function

@object_type
class MyModule:

    @function
    def hello(name: str | None = None) -> str:
        if name != None:
            return f"Hello, {name}"
        return "Hello, world"
