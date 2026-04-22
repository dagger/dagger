from dagger import function, object_type


@object_type
class MyModule:
    @function
    async def add_integer(self, a: int, b: int) -> int:
        return a + b
