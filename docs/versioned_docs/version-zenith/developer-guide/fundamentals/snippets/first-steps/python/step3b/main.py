from dagger import field, function, object_type


@object_type
class PotatoMessage:
    message: str = field()
    from_: str = field(name="from")


@function
def hello_world(message: str) -> PotatoMessage:
    return PotatoMessage(
        message=message,
        from_="potato@example.com",
    )
