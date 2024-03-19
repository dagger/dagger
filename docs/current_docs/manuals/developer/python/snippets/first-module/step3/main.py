from dagger import function, object_type


@object_type
class Potato:
    @function
    def hello_world(self, count: int, mashed: bool = False) -> str:
        if mashed:
            return f"Hello Daggernauts, I have mashed {count} potatoes"
        return f"Hello Daggernauts, I have {count} potatoes"
