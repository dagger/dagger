package io.dagger.client;

import io.dagger.client.engineconn.Connection;
import java.io.IOException;

public class Dagger {
  private static Session dag = null;

  /**
   * Returns the global Dagger session.
   *
   * <p>Contrary to {@code connect}, this is managed as a singleton. It will always return the same
   * instance. Synchronized because the first call may start an engine session, and two threads
   * racing it would start two.
   *
   * @return Global Dagger session
   */
  public static synchronized Session dag() {
    if (dag == null) {
      try {
        dag = new Session(Connection.get(System.getProperty("user.dir")));
      } catch (IOException e) {
        throw new RuntimeException("Could not connect to Dagger engine", e);
      }
    }
    return dag;
  }

  /**
   * Opens connection with a Dagger engine.
   *
   * @return The Dagger session
   * @throws IOException
   */
  public static Session connect() throws IOException {
    return connect(System.getProperty("user.dir"), false);
  }

  /**
   * Opens connection with a Dagger engine.
   *
   * @param loadWorkspaceModules whether to opt into loading workspace modules
   * @return The Dagger session
   * @throws IOException
   */
  public static Session connect(boolean loadWorkspaceModules) throws IOException {
    return connect(System.getProperty("user.dir"), loadWorkspaceModules);
  }

  /**
   * Opens connection with a Dagger engine.
   *
   * @param workingDir the host working directory
   * @return The Dagger session
   * @throws IOException
   */
  public static Session connect(String workingDir) throws IOException {
    return connect(workingDir, false);
  }

  /**
   * Opens connection with a Dagger engine.
   *
   * @param workingDir the host working directory
   * @param loadWorkspaceModules whether to opt into loading workspace modules
   * @return The Dagger session
   * @throws IOException
   */
  public static Session connect(String workingDir, boolean loadWorkspaceModules)
      throws IOException {
    return new Session(Connection.get(workingDir, loadWorkspaceModules));
  }
}
