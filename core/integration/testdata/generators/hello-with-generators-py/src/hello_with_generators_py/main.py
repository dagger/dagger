from os import error

import dagger
from dagger import dag, function, generate, object_type


@object_type
class HelloWithGeneratorsPy:
    @function
    @generate
    def generate_files(self) -> dagger.Changeset:
        """Return a changeset with a new file"""
        return dag.directory().with_new_file("foo", "bar").changes(dag.directory())

    @function
    @generate
    def generate_other_files(self) -> dagger.Changeset:
        """Return a changeset with a new file"""
        return dag.directory().with_new_file("bar", "foo").changes(dag.directory())

    @function
    @generate
    def empty_changeset(self) -> dagger.Changeset:
        """Return an empty changeset"""
        return dag.directory().changes(dag.directory())

    @function
    @generate
    def changeset_failure(self) -> dagger.Changeset:
        """Return an error"""
        raise Exception("could not generate the changeset")
