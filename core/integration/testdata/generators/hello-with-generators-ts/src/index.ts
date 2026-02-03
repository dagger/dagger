import { dag, Changeset, object, func, generate } from "@dagger.io/dagger";

@object()
export class HelloWithGeneratorsTs {
  /**
   * Return a changeset with a new file
   */
  @func()
  @generate()
  generateFiles(): Changeset {
    return dag.directory().withNewFile("foo", "bar").changes(dag.directory());
  }

  /**
   * Return a changeset with a new file
   */
  @func()
  @generate()
  generateOtherFiles(): Changeset {
    return dag.directory().withNewFile("bar", "foo").changes(dag.directory());
  }

  /**
   * Return an empty changeset
   */
  @func()
  @generate()
  emptyChangeset(): Changeset {
    return dag.directory().changes(dag.directory());
  }

  /**
   * Return an error
   */
  @func()
  @generate()
  changesetFailure(): Changeset {
    throw "could not generate the changeset";
  }
}
