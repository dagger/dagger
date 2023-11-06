from dagger.mod import function


@function
def hello_world() -> str:
    return "Hello daggernauts!"
