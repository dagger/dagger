"""Example with name overrides"""
from typing import Annotated

import dagger
from dagger import Arg, Doc, dag, field, function, object_type


@object_type
class MyModule:
    def_: Annotated[str, Doc("Definition")] = field(name="def", default="latest")

    @function(name="import")
    def import_(
        self,
        from_: Annotated[str, Arg(name="from"), Doc("Image ref")] = "alpine",
    ) -> dagger.Container:
        """Import the specified image"""
        return dag.container().with_label("definition", self.def_).from_(from_)
