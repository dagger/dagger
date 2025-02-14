package io.dagger.modules.defaults;

import io.dagger.client.DaggerQueryException;
import io.dagger.client.Directory;
import io.dagger.client.File;
import io.dagger.module.AbstractModule;
import io.dagger.module.annotation.Default;
import io.dagger.module.annotation.DefaultPath;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Ignore;
import io.dagger.module.annotation.Object;
import java.util.concurrent.ExecutionException;

@Object
public class Defaults extends AbstractModule {
  public Defaults() {
    super();
  }

  @Function
  public String echo(@Default("default value") String value) {
    return value;
  }

  @Function
  public String fileName(@DefaultPath("dagger.json") File file)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    return file.name();
  }

  @Function
  public String fileNames(@DefaultPath("src/main/java/io/dagger/modules/defaults") Directory dir)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    return String.join(" ", dir.entries());
  }

  @Function
  public String filesNoIgnore(@DefaultPath(".") Directory dir)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    return String.join(" ", dir.entries());
  }

  @Function
  public String filesIgnore(@DefaultPath(".") @Ignore({"*.xml"}) Directory dir)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    return String.join(" ", dir.entries());
  }

  @Function
  public String filesNegIgnore(@DefaultPath(".") @Ignore({"**", "!**/*.java"}) Directory dir)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    return String.join(" ", dir.entries());
  }
}
