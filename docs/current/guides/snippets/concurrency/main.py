import secrets
import sys

import anyio

import dagger


async def long_time_task(c: dagger.Client):
    """
    a task that can take a long time.

    :param c: dagger client.
    """
    await c.container() \
        .from_("alpine") \
        .with_exec(["sleep", str(secrets.randbelow(10))]) \
        .with_exec(["echo", "task done"]) \
        .sync()


async def main():
    """Execute multiple tasks in concurrency."""
    async with \
            dagger.Connection(dagger.Config(log_output=sys.stderr)) as client, \
            anyio.create_task_group() as tg:
        tg.start_soon(long_time_task, client)
        tg.start_soon(long_time_task, client)
        tg.start_soon(long_time_task, client)


anyio.run(main)
