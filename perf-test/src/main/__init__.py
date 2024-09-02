import anyio

import dagger


@dagger.object_type
class PerfTest:
    @dagger.function
    async def sleep(self, duration: int = 60 * 5):
        await anyio.sleep(duration)
