from dagger import function, object_type


@object_type
class MyModule:
    @function
    async def add_float(self, a: float, b: float) -> float:
        return a + b
