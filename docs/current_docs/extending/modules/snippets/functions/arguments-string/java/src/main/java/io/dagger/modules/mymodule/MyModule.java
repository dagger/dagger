package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;
import io.dagger.client.exception.DaggerQueryException;


import io.dagger.client.exception.DaggerExecException;
import io.dagger.client.exception.DaggerQueryException;


import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;
 public class MyModule {unction String getUser(String gnder)rows ExecutionException,DaggerExecException,DaggerQueryException,InterruptedException {g().container().from("alp         .withExec(List.of("apk", "add", "jq"))
        .withExec(
            List.of(
                "sh",
                "-c",
                "curl https://randomuser.me/api/?gender=%s | jq .results[0].name"
                    .formatted(gender)))
        .stdout();
  }
}
