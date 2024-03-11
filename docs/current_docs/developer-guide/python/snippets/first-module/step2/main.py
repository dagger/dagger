from dagger import function, object_type


@object_type
class Potato:
    @function
    def hello_world(self) -> str:
        return "Hello Daggernauts!"
