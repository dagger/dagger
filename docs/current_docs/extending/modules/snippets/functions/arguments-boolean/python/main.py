from dagger import function, object_type


@object_type
class MyModule:
    @function
    def hello(self, shout: bool) -> str:
        message = "Hello, world"
        if shout:
            return message.upper()
        return message
