import anyio

import dagger
from dagger import dag, function, object_type


@object_type
class Tests:
    @function
    async def all(self):
        async with anyio.create_task_group() as tg:
            tg.start_soon(self.hello)
            tg.start_soon(self.custom_greeting)

    @function
    async def all_manual(self):
        await self.hello()
        await self.custom_greeting()

    @function
    async def hello(self):
        greeting = await dag.greeter().hello("World")

        if greeting != "Hello, World!":
            raise Exception("unexpected greeting")

    @function
    async def custom_greeting(self):
        greeting = await dag.greeter(greeting = "Welcome").hello("World")

        if greeting != "Welcome, World!":
            raise Exception("unexpected greeting")
