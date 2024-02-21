import dagger
from dagger import function

@function
def hello(name: str = "world") -> str:
    return f"Hello, {name}"
