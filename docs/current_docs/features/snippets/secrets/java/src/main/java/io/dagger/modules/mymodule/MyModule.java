package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.exception.DaggerExecException;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.client.Secret;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

@Object public class MyModule {unction String githubApi(Secrettoken)rows ExecutionException,DaggerExecException,DaggerQueryException,InterruptedException {g().container().from("alp         .withExec(List.of("apk", "add", "curl"))
        .withExec(
            List.of(
                "sh",
                "-c",
                "curl \"https://api.github.com/repos/dagger/dagger/issues\""
                    + " --header \"Authorization: Bearer $GITHUB_API_TOKEN\""
                    + " --header \"Accept: application/vnd.github+json\""))
        .stdout();
  }
}
