import random
import dagger
from dagger import dag, object_type

@object_type
class MyModule:
    def build_base_image(self, source: dagger.Directory) -> dagger.Container:
        """Build base image"""
        return (
            dag.node()
            .with_version("21")
            .with_npm()
            .with_source(source)
            .install([])
            .container()
        )
