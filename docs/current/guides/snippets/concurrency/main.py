import sys
import random


import anyio

import dagger

async def longTimeTask(c: dagger.Client):
    await c.container()\
        .from_("alpine")\
        .with_exec([ "sleep", str(random.randint(0, 10)) ])\
        .with_exec([ "echo", "task done" ])\
        .sync()


async def main():
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        async with anyio.create_task_group() as tg:
            tg.start_soon(longTimeTask, client)
            tg.start_soon(longTimeTask, client)
            tg.start_soon(longTimeTask, client)


anyio.run(main)
