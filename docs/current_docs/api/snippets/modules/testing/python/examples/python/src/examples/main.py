import anyio

import dagger
from dagger import dag, function, object_type


@object_type
class Examples:
    @function
    async def all(self):
        async with anyio.create_task_group() as tg:
            tg.start_soon(self.greeter_hello)
            tg.start_soon(self.greeter__custom_greeting)

    @function
    async def greeter_hello(self):
        greeting = await dag.greeter().hello("World")

    	# Do something with the greeting

    @function
    async def greeter__custom_greeting(self):
        greeting = await dag.greeter(greeting = "Welcome").hello("World")

    	# Do something with the greeting
