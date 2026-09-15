from dagger import function, object_type


@object_type
class Test:
    simple_value: str
    http_url: str

    @function
    def constructor_values(self) -> str:
        return f"{self.simple_value}|{self.http_url}"

    @function
    def echo(self, snake_case: str, http_url: str) -> str:
        return f"{snake_case}|{http_url}"
