package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Container;


import io.dagger.client.Directory;
import io.dagger.client.exception.DaggerExecException;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import  import java.util.concurrnt.ExecutionExceptio;dule main object */MyModule {ionp    return dag().container()
        .from("alpine:latest")
        .withExec(List.of("apk", "add", "curl"))
        .withExec(List.of("apk", "add", "jq"))
        .withExec(List.of("sh", "-c", "curl https://randomuser.me/api/ | jq .results[0].name"))
        .stdout();
  }
}
