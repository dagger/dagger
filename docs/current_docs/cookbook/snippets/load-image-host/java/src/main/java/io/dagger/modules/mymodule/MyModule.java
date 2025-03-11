package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Container;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.client.Socket;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {
  @Function
  public Container load(Socket docker, String tag) throws ExecutionException, DaggerQueryException, InterruptedException {
    // Create a new container
    Container ctr = dag().container().from("alpine").withExec(List.of("apk", "add", "git"));

    // Create a new container from the dockerCLI image
    // Mount the docker socket from the host
    // Mount the newly-built container as a tarball
    Container dockerCli = dag().container()
            .from("docker:cli")
            .withUnixSocket("/var/run/docker.sock", docker)
            .withMountedFile("image.tar", ctr.asTarball());

    // load the image from the tarball
    String out = dockerCli
            .withExec(List.of("docker", "load", "-i", "image.tar"))
            .stdout();

    // Add the tag to the image
    String image = out.split(":", 2)[1].trim();
    return dockerCli
            .withExec(List.of("docker", "tag", image, tag))
            .sync();
  }
}
