import dagger
from dagger import function, object_type


@object_type
class Test:
    secret_with_default: dagger.Secret | None = None

    @function
    async def check(self) -> str:
        if self.secret_with_default is None:
            return "secret is None"
        val = await self.secret_with_default.plaintext()
        return f"secret is: {val}"
