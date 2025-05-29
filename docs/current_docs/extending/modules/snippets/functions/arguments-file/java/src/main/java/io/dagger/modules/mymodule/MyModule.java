package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.exception.DaggerExecException;
import io.dagger.client.exception.DaggerQueryException;


import io.dagger.client.File;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;

 @Objectic class MyModule {ion String readFile(File sorce)ExecutionException,DaggerExecException,DaggerQueryException,InterruptedException {rn dag().c         .withFile("/src/myfile", source)
        .withExec(List.of("cat", "/src/myfile"))
        .stdout();
  }
}
