package io.dagger.sample;

import io.dagger.client.Client;
import io.dagger.client.Client.ContainerArguments;
import io.dagger.client.Container;
import io.dagger.client.Container.WithNewFileArguments;
import io.dagger.client.Dagger;
import io.dagger.client.Platform;
import io.dagger.client.Secret;

@Description("Publish a container image to a remote registry")
public class PublishImage {

  public static void main(String... args) throws Exception {
    String username = System.getenv("DOCKERHUB_USERNAME");
    String password = System.getenv("DOCKERHUB_PASSWORD");
    if (username == null) {
      username = new String(System.console().readLine("Docker Hub username: "));
    }
    if (password == null) {
      password = new String(System.console().readPassword("Docker Hub password: "));
    }
    try (Client client = Dagger.connect()) {
      // set secret as string value
      Secret secret = client.setSecret("password", password);

      Container c =
          client
              .container(new ContainerArguments().withPlatform(Platform.from("linux/amd64")))
              .from("nginx:1.23-alpine")
              .withNewFile(
                  "/usr/share/nginx/html/index.html",
                  "Hello from Dagger!",
                  new WithNewFileArguments().withPermissions(0400));

      // use secret for registry authentication
      String addr =
          c.withRegistryAuth("docker.io", username, secret).publish(username + "/my-nginx");

      // print result
      System.out.println("Published at: " + addr);
    }
  }
}
