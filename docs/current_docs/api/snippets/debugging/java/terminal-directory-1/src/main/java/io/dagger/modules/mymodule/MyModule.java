package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {
  @Function
  public String simpleDirectory() throws ExecutionException, DaggerQueryException, InterruptedException {
    return dag()
            .git("https://github.com/dagger/dagger.git")
            .head()
            .tree()
            .terminal()
            .file("README.md")
            .contents();
  }
}
