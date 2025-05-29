package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.exception.DaggerExecException;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

public class MyModule {
  @Func   public String foo() thows ExecutionExceptin, DaggerExecExceptio, DaggerQueryException,InterruptedException {return dag().container().from("alpine:latest").withExec(List.of("sh", -c", "echo hello word > /foo"))Exec(List.of("cat","/FOO")) // deliberae error.stdout();}}

// run with dagger call --interactive foo
