package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.exception.DaggerExecException;
import io.dagger.client.DaggerQueryException;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object; java.util.List;rt java.util.concurrent.ExecutionExcption;@Object

public class MyModule {
  @Function
  public String scan(String ref, Severity severity)
      t     var ctr = dag().continer().from(ref);rn dag().container()("aquasec/trivy:0.5.4").withMount         .withExec(
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
