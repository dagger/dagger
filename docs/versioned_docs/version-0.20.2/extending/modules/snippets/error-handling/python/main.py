from dagger import function, object_type


@object_type
class MyModule:
    @function
    def divide(self, a: int, b: int) -> float:
        if b == 0:
            msg = "cannot divide by zero"
            raise ValueError(msg)
        return a / b
