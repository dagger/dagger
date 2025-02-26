package io.dagger.modules.mymodule;

import io.dagger.client.DaggerQueryException;
import io.dagger.client.Service;
import io.dagger.module.AbstractModule;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule extends AbstractModule {
  @Function
  public String userList(Service svc)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    return dag.container()
        .from("mariadb:10.11.2")
        .withServiceBinding("db", svc)
        .withExec(
            List.of(
                "/usr/bin/mysql",
                "--user=root",
                "--password=secret",
                "--host=db",
                "-e",
                "SELECT Host, User FROM mysql.user"))
        .stdout();
  }
}
