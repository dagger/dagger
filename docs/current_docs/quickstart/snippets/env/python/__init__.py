import dagger
from dagger import dag, function, object_type


@object_type
class HelloDagger:
    @function
    def build_env(self, source: dagger.Directory) -> dagger.Container:
        """Returns a container with the build environment"""
        # create a Dagger cache volume for dependencies
        node_cache = dag.cache_volume("node")
        return (
            dag.container()
            # start from a base Node.js container
            .from_("node:21-slim")
            # add the source code at /src
            .with_directory("/src", source)
            # mount the cache volume at /src/node_modules
            .with_mounted_cache("/src/node_modules", node_cache)
            # change the working directory to /src
            .with_workdir("/src")
            # run npm install to install dependencies
            .with_exec(["npm", "install"])
        )
