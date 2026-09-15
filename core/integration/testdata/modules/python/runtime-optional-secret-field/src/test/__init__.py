import base64

import dagger
from dagger import field, function, object_type


@object_type
class Test:
    @function
    def getobj(self, *, top_secret: dagger.Secret | None = None) -> "Obj":
        return Obj(top_secret=top_secret)


@object_type
class Obj:
    top_secret: dagger.Secret | None = field(default=None)

    @function
    async def get_secret(self) -> str:
        plaintext = await self.top_secret.plaintext()
        return base64.b64encode(plaintext.encode()).decode()
