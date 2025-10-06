import os
import sys

from anyio import run_process

import dagger


@dagger.object_type
class Caller:
    @dagger.function
    async def test(self, args: list[str] | None = None):
        if args is None:
            args = []
        cmd = ("pytest", "--pyargs", "caller", "--numprocesses", "logical", *args)
        await run_process(cmd, stdout=sys.stdout, stderr=sys.stderr, env=os.environ)
