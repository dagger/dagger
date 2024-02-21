import dagger
from dagger import function

@function
def hello(name: str | None = None) -> str:
    if name != None:
        return f"Hello, {name}"
    return "Hello, world"
