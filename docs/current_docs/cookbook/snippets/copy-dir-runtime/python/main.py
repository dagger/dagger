import dagger
from dagger import function, object_type


@object_type
class MyModule:
    @function
    async def copy_file(self, source: dagger.File):
        await source.export("foo.txt")
        # your custom logic here
        # for example, read and print the file in the Dagger Engine container
        print(open("foo.txt").read())
