package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.time.ZonedDateTime;
import java.time.format.DateTimeFormatter;
import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {
  /** Run a build with cache invalidation */
  @Function
  public String build() throws ExecutionException, DaggerQueryException, InterruptedException {
    return dag()
        .container()
        .from("alpine")
        // comment out the line below to see the cached date output
        .withEnvVariable("CACHEBUSTER", ZonedDateTime.now().format(DateTimeFormatter.ISO_DATE_TIME))
        .withExec(List.of("date"))
        .stdout();
  }
}
