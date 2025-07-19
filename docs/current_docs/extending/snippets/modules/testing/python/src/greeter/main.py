from dagger import function, object_type


@object_type
class Greeter:
    greeting: str = "Hello"

    @function
    def hello(self, name: str) -> str:
        """Greets the provided name"""
        return f"{self.greeting}, {name}!"
