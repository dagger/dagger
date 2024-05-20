import random

import dagger
from dagger import function, object_type


@object_type
class HelloDagger:
    @function
    async def publish(self, source: dagger.Directory) -> str:
        """Tests, builds and publishes the application"""
        # call Dagger Function to run unit tests
        self.test(source)
        # call Dagger Function to build the application image
        # publish the image to ttl.sh
        return await self.build(source).publish(
            f"ttl.sh/myapp-{random.randrange(10 ** 8)}"
        )
