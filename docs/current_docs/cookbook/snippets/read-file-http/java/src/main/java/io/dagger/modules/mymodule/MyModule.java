package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.*;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.Optional;

@Object
public class MyModule {
  @Function
  public Container readFileHttp(String url) {
    File file = dag().http(url);
    return dag().container().from("alpine:latest").withFile("/src/myfile", file);
  }
}
