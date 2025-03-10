package com.mycompany.app;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.*;
import java.util.List;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.atomic.AtomicBoolean;

public class Test {
  public static void main(String[] args) throws Exception {
    try {
      // get reference to the local project
      Directory src = dag().host().directory(".");

      AtomicBoolean hasFailure = new AtomicBoolean(false);

      List.of(17, 21, 23)
          .forEach(
              javaVersion -> {
                Container mvn =
                    dag()
                        .container()
                        .from("maven:3.9.9-eclipse-temurin-%d".formatted(javaVersion))
                        .withDirectory("/src", src)
                        .withExec(
                            List.of("mvn", "test"),
                            // do not fail on errors so we can print the output
                            new Container.WithExecArguments().withExpect(ReturnType.ANY));

                System.out.printf(
                    "%n==> Starting tests with java compiler version %d...%n", javaVersion);
                try {
                  if (mvn.exitCode() != 0) {
                    hasFailure.set(true);
                  }
                  System.out.println(mvn.stdout());
                } catch (InterruptedException | ExecutionException | DaggerQueryException e) {
                  throw new RuntimeException(e);
                }
                System.out.printf(
                    "==> Completed tests with java compiler version %d...%n", javaVersion);
              });
      if (hasFailure.get()) {
        throw new RuntimeException("Some tests failed");
      }
    } finally {
      dag().close();
    }
  }
}
