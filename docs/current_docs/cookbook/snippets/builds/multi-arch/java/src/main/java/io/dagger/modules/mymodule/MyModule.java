package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.*;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {

  private Container builder;

  /**
   * Build and publish multi-platform image
   *
   * @param src source code location. Can be local directory or remote Git repository
   */
  @Function
  public String build(Directory src)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    // platforms to build for and push in a multi-platform image
    Platform[] platforms = {
      Platform.from("linux/amd64"), // a.k.a. x86_64
      Platform.from("linux/arm64"), // a.k.a. aarch64
      Platform.from("linux/s390x") // a.k.a. IBM S/390
    };

    // container registry for the multi-platform image
    String imageRepo = "ttl.sh/myapp:latest";

    List<Container> platformVariants = new ArrayList<>();
    for (Platform platform : platforms) {
      // pull golang image for this platform
      Container ctr =
          dag()
              .container(new Client.ContainerArguments().withPlatform(platform))
              .from("golang:1.20-alpine")
              // mount source code
              .withDirectory("/src", src)
              // mount empty dir where built binary will live
              .withDirectory("/output", dag().directory())
              // ensure binary will be statically linked and thus executable in the final image
              .withEnvVariable("CGO_ENABLED", "0")
              // build binary and put result at mounted output directory
              .withWorkdir("/src")
              .withExec(List.of("go", "build", "-o", "/output/hello"));

      // select output directory
      Directory outputDir = ctr.directory("/output");

      // wrap the output directory in the new empty container marked with the same platform
      Container binaryCtr =
          dag()
              .container(new Client.ContainerArguments().withPlatform(platform))
              .withRootfs(outputDir);

      platformVariants.add(binaryCtr);
    }

    return dag()
        .container()
        .publish(
            imageRepo, new Container.PublishArguments().withPlatformVariants(platformVariants));
  }
}
