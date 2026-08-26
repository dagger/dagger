import random
from typing import Annotated

import dagger
from dagger import DefaultPath, function, object_type


@object_type
class HelloDagger:
    @function
    async def publish(
        self,
        source: Annotated[dagger.Directory, DefaultPath("/")],
    ) -> str:
        """Publish the application container after building and testing it on-the-fly"""
        # call Dagger Function to run unit tests
        await self.test(source)
        # call Dagger Function to build the application image
        # publish the image to ttl.sh
        return await self.build(source).publish(
            f"ttl.sh/hello-dagger-{random.randrange(10**8)}"
        )
