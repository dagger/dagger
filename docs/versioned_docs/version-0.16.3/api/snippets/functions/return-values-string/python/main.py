import dagger
from dagger import function, object_type


@object_type
class MyModule:
    @function
    async def os_info(self, ctr: dagger.Container) -> str:
        return await ctr.with_exec(["uname", "-a"]).stdout()
