package io.dagger.modules.hellowithchecksjava;

import io.dagger.client.Container;
import io.dagger.client.Dagger;
import io.dagger.module.annotation.Check;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;

@Object
public class Test {

  @Function
  @Check
  public void lint() throws Exception {
    Container container = Dagger.dag()
      .container()
      .from("alpine")
      .withExec(List.of("sh", "-c", "exit 0"));
    container.sync();
  }

  @Function
  @Check
  public void unit() throws Exception {
    Container container = Dagger.dag()
      .container()
      .from("alpine")
      .withExec(List.of("sh", "-c", "exit 0"));
    container.sync();
  }
}
