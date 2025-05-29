package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Container;
import io.dagger.client.exception.DaggerExecException;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.client.Service;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;
 @Objectic class MyModule {ion Service services() thros ExecutionException DaggerExecException,DaggerQueryException, InterruptedException {vcA = dag().containr().from("ngi         .asService(new Container.AsServiceArguments()
            .withArgs(List.of("sh", "-c", "nginx & while true; do curl svcb:80 && sleep 1; done")))
        .withHostname("svca");
    svcA.start();
    Service svcB = dag().container()
        .from("nginx")
        .withExposedPort(80)
        .asService(new Container.AsServiceArguments()
            .withArgs(List.of("sh", "-c", "nginx & while true; do curl svca:80 && sleep 1; done")))
        .withHostname("svcb");
    svcB.start();
    return svcB;
  }
}
