from dagger import field, object_type


@object_type
class Test:
    foo: str = field()

    def __init__(self):
        raise ValueError("too bad: " + "so sad")
