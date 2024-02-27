from dagger import field, function, object_type


@object_type
class PotatoMessage:
    message: str = field()
    from_: str = field(name="from")


@object_type
class Potato:

    @function
    def hello_world(
        self,
        count: int,
        mashed: bool = False,
    ) -> PotatoMessage:
        if mashed:
            message = f"Hello Daggernauts, I have mashed {count} potatoes"
        else:
            message = f"Hello Daggernauts, I have {count} potatoes"

        return PotatoMessage(
            message=message,
            from_="potato@example.com",
        )
