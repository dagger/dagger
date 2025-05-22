package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Client;
import io.dagger.client.Container;
import io.dagger.client.Directory;
import io.dagger.client.Socket;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;

/** Demonstrates an SSH-based clone requiring a user-supplied SSHAuthSocket */
@Object
public class MyModule {
  @Function
  public Container cloneWithSsh(String repository, String ref, Socket sock) {
    Directory d =
        dag().git(repository, new Client.GitArguments().withSshAuthSocket(sock)).ref(ref).tree();

    return dag().container().from("alpine:latest").withDirectory("/src", d).withWorkdir("/src");
  }
}
