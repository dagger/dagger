package io.dagger.modules.mymodule;

import io.dagger.client.*;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {
  /** Copy a file to the Dagger module runtime container for custom processing */
  @Function
  public File copyFile(File source)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    source.export("foo.txt");
    java.nio.file.Path filePath = java.nio.file.Paths.get("foo.txt");
    try {
      String content = java.nio.file.Files.readString(filePath);
      System.out.println("File content:\n" + content);
    } catch (java.io.IOException e) {
      System.err.println("Failed to read file content: " + e.getMessage());
    }

    return source;
  }
}
