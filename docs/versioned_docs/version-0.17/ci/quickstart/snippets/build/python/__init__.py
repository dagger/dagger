from typing import Annotated

import dagger
from dagger import DefaultPath, dag, function, object_type


@object_type
class HelloDagger:
    @function
    def build(
        self,
        source: Annotated[dagger.Directory, DefaultPath("/")],
    ) -> dagger.Container:
        """Build the application container"""
        build = (
            # get the build environment container
            # by calling another Dagger Function
            self.build_env(source)
            # build the application
            .with_exec(["npm", "run", "build"])
            #  get the build output directory
            .directory("./dist")
        )
        return (
            dag.container()
            # start from a slim NGINX container
            .from_("nginx:1.25-alpine")
            # copy the build output directory to the container
            .with_directory("/usr/share/nginx/html", build)
            # expose the container port
            .with_exposed_port(80)
        )
