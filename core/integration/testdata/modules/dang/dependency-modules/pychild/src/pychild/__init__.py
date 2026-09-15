from dagger import function, object_type


@object_type
class Pychild:
    @function
    def value(self) -> str:
        return "python"
