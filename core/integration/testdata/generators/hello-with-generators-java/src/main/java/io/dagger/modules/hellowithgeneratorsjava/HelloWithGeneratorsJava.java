package io.dagger.modules.hellowithgeneratorsjava;

import io.dagger.client.Changeset;
import io.dagger.client.Dagger;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Generate;
import io.dagger.module.annotation.Object;

/** A module for HelloWithGeneratorsJava functions */
@Object
public class HelloWithGeneratorsJava {

  /** Return a changeset with a new file */
  @Function
  @Generate
  public Changeset generateFiles() {
    return Dagger.dag()
      .directory()
      .withNewFile("foo", "bar")
      .changes(Dagger.dag().directory());
  }

  /** Return a changeset with a new file */
  @Function
  @Generate
  public Changeset generateOtherFiles() {
    return Dagger.dag()
      .directory()
      .withNewFile("bar", "foo")
      .changes(Dagger.dag().directory());
  }

  /** Return an empty changeset */
  @Function
  @Generate
  public Changeset emptyChangeset() {
    return Dagger.dag().directory().changes(Dagger.dag().directory());
  }

  /** Return an error */
  @Function
  @Generate
  public Changeset changesetFailure() throws Exception {
    throw new Exception("could not generate the changeset");
  }

  @Function
  public MetaGen otherGenerators() {
    return new MetaGen();
  }
}
