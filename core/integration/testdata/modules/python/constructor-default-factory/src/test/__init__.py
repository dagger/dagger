import dagger
from dagger import dag, field, object_type


@object_type
class Test:
    foo: dagger.File = field(
        default=lambda: (
            dag.directory()
            .with_new_file("foo.txt", "default factory content")
            .file("foo.txt")
        )
    )
    bar: list[str] = field(default=list)
