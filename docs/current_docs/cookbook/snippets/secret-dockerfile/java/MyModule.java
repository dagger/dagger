package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Container;
import io.dagger.client.DaggerQueryException;
import io.dagger.client.Directory;
import io.dagger.client.Secret;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {
  /**
   * Build a Container from a Dockerfile
   *
   * @param source The source code to build
   * @param secret The secret to use in the Dockerfile
   */
  @Function
  public Container build(Directory source, Secret secret)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    // Ensure the Dagger secret's name matches what the Dockerfile expected as the id for the secret
    // mount.
    String secretVal = secret.plaintext();

    Secret buildSecret = dag().setSecret("gh-secret", secretVal);

    return source.dockerBuild(
        new Directory.DockerBuildArguments().withSecrets(List.of(buildSecret)));
  }
}
