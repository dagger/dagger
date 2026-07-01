package io.dagger.client;

import io.dagger.client.engineconn.Connection;
import java.io.IOException;

public class Dagger {
  private static Client dag = null;

  /**
   * Returns the global Dagger client instance.
   *
   * <p>Contrary to {@code connect}, this is managed as a singleton. It will always return the same
   * instance.
   *
   * @return Global Dagger client
   */
  public static Client dag() {
    if (dag == null) {
      try {
        dag = new Client(Connection.get(System.getProperty("user.dir")));
      } catch (IOException e) {
        throw new RuntimeException("Could not connect to Dagger engine", e);
      }
    }
    return dag;
  }

  /**
   * Opens connection with a Dagger engine.
   *
   * @return The Dagger API entrypoint
   * @throws IOException
   */
  public static AutoCloseableClient connect() throws IOException {
    return connect(System.getProperty("user.dir"), false);
  }

  /**
   * Opens connection with a Dagger engine.
   *
   * @param loadWorkspaceModules whether to opt into loading workspace modules
   * @return The Dagger API entrypoint
   * @throws IOException
   */
  public static AutoCloseableClient connect(boolean loadWorkspaceModules) throws IOException {
    return connect(System.getProperty("user.dir"), loadWorkspaceModules);
  }

  /**
   * Opens connection with a Dagger engine.
   *
   * @param workingDir the host working directory
   * @return The Dagger API entrypoint
   * @throws IOException
   */
  public static AutoCloseableClient connect(String workingDir) throws IOException {
    return connect(workingDir, false);
  }

  /**
   * Opens connection with a Dagger engine.
   *
   * @param workingDir the host working directory
   * @param loadWorkspaceModules whether to opt into loading workspace modules
   * @return The Dagger API entrypoint
   * @throws IOException
   */
  public static AutoCloseableClient connect(String workingDir, boolean loadWorkspaceModules)
      throws IOException {
    return new AutoCloseableClient(Connection.get(workingDir, loadWorkspaceModules));
  }
}
