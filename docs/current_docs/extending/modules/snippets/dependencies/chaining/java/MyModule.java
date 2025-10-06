package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Directory;
import io.dagger.client.Golang;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;

@Object
public class MyModule {
  @Function
  public Directory example(Directory buildSrc, List<String> buildArgs) {
    return dag()
        .golang()
        .build(buildArgs, new Golang.BuildArguments().withSource(buildSrc))
        .terminal();
  }
}
