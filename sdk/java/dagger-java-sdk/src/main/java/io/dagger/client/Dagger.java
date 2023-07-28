package io.dagger.client;

import io.dagger.client.engineconn.Connection;
import java.io.IOException;

public class Dagger {

  /**
   * Opens connection with a Dagger engine.
   *
   * @return The Dagger API entrypoint
   * @throws IOException
   */
  public static Client connect() throws IOException {
    return connect(System.getProperty("user.dir"));
  }

  /**
   * Opens connection with a Dagger engine.
   *
   * @param workingDir the host working directory
   * @return The Dagger API entrypoint
   * @throws IOException
   */
  public static Client connect(String workingDir) throws IOException {
    return new Client(Connection.get(workingDir));
  }
}
