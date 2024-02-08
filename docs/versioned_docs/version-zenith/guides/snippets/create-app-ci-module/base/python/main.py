import random
import dagger
from dagger import dag, object_type, field, function

@object_type
class MyModule:
    source: dagger.Directory = field()

    # build base image
    def build_base_image(self) -> dagger.Container:
        return (
            dag.node()
            .with_version("21")
            .with_npm()
            .with_source(self.source)
            .install([])
            .container()
        )
