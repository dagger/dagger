from typing import Annotated

from dagger import Arg, function


@function
def fn(id_: Annotated[str, Arg("id")]) -> str:
    return id_
