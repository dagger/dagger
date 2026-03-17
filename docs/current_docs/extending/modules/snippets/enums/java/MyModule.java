package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {
  @Function
  public String scan(String ref,Severity severity) throws ExecutionException, DaggerQueryException, InterruptedException {
    var ctr = dag().container().from(ref);

    return dag()
        .container()
        .from("aquasec/trivy:0.50.4")
        .withMountedFile("/mnt/ctr.tar", ctr.asTarball())
        .withMountedCache("/root/.cache", dag().cacheVolume("trivy-cache"))
        .withExec(
            List.of(
                "trivy",
                "image",
                "--format=json",
                "--no-progress",
                "--exit-code=1",
                "--vuln-type=os,library",
                "--severity=" + severity.name(),
                "--show-suppressed",
                "--input=/mnt/ctr.tar"))
        .stdout();
  }
}
