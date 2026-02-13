package io.dagger.modules.hellowithgeneratorsjava;

import io.dagger.client.Changeset;
import io.dagger.client.Dagger;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Generate;
import io.dagger.module.annotation.Object;

@Object
public class MetaGen {

  @Function
  @Generate
  public Changeset genThings() {
    return Dagger.dag()
        .directory()
        .withNewFile("meta-gen", "generated")
        .changes(Dagger.dag().directory());
  }
}
