package io.dagger.client.engineconn;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.CALLS_REAL_METHODS;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.mockStatic;
import static org.mockito.Mockito.never;
import static org.mockito.Mockito.times;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

import java.io.IOException;
import java.util.Optional;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.mockito.MockedStatic;
import uk.org.webcompere.systemstubs.environment.EnvironmentVariables;
import uk.org.webcompere.systemstubs.jupiter.SystemStub;
import uk.org.webcompere.systemstubs.jupiter.SystemStubsExtension;

@ExtendWith(SystemStubsExtension.class)
public class ConnectionTest {

  @SystemStub private EnvironmentVariables environmentVariables;

  @Test
  public void should_return_from_env_connection() throws Exception {
    environmentVariables.set("DAGGER_SESSION_PORT", "52037");
    environmentVariables.set("DAGGER_SESSION_TOKEN", "189de95f-07df-415d-b42a-7851c731359d");
    Optional<Connection> conn = Connection.fromEnv();
    assertThat(conn).isPresent();
  }

  @Test
  public void should_return_empty_connection_when_env_not_set() throws Exception {
    Optional<Connection> conn;

    environmentVariables.set("DAGGER_SESSION_PORT", null);
    environmentVariables.set("DAGGER_SESSION_TOKEN", null);
    conn = Connection.fromEnv();
    assertThat(conn).isEmpty();

    environmentVariables.set("DAGGER_SESSION_PORT", "52037");
    environmentVariables.set("DAGGER_SESSION_TOKEN", null);
    conn = Connection.fromEnv();
    assertThat(conn).isEmpty();

    environmentVariables.set("DAGGER_SESSION_PORT", null);
    environmentVariables.set("DAGGER_SESSION_TOKEN", "189de95f-07df-415d-b42a-7851c731359d");
    conn = Connection.fromEnv();
    assertThat(conn).isEmpty();
  }

  @Test
  public void should_provision_cli_for_independent_sessions_without_inherited_token() {
    environmentVariables.set("DAGGER_NESTING", "INDEPENDENT_SESSIONS");
    environmentVariables.set("DAGGER_SESSION_PORT", "52037");
    environmentVariables.set("DAGGER_SESSION_TOKEN", null);

    assertThat(Connection.fromEnv()).isEmpty();
  }

  @Test
  public void should_reject_invalid_dagger_nesting_environment() {
    environmentVariables.set("DAGGER_NESTING", "UNKNOWN");
    assertThatThrownBy(Connection::fromEnv)
        .isInstanceOf(IllegalStateException.class)
        .hasMessageContaining("unknown DAGGER_NESTING");

    environmentVariables.set("DAGGER_NESTING", "INDEPENDENT_SESSIONS");
    environmentVariables.set("DAGGER_SESSION_PORT", null);
    assertThatThrownBy(Connection::fromEnv)
        .isInstanceOf(IllegalStateException.class)
        .hasMessageContaining("requires DAGGER_SESSION_PORT");
  }

  @Test
  public void should_return_connection_from_dynamic_provisioning() throws Exception {
    CLIRunner runner = mock(CLIRunner.class);
    when(runner.getConnectionParams())
        .thenReturn(new ConnectParams(57535, "6fb6d80b-5e7a-42f7-913c-31a6e50c140d"));
    Connection conn = Connection.fromCLI(runner);
    verify(runner, times(1)).getConnectionParams();
    conn.close();
    verify(runner, times(1)).shutdown();
  }

  @Test
  public void should_not_call_dynamic_provisioning_when_env_vars_are_present() throws Exception {
    MockedStatic<Connection> connectionMockedStatic =
        mockStatic(Connection.class, CALLS_REAL_METHODS);
    environmentVariables.set("DAGGER_SESSION_PORT", "52037");
    environmentVariables.set("DAGGER_SESSION_TOKEN", "189de95f-07df-415d-b42a-7851c731359d");
    Connection conn = Connection.get("/tmp");
    assertThat(conn).isNotNull();
    connectionMockedStatic.verify(() -> Connection.fromCLI(any()), never());
  }

  @Test
  public void should_fail_when_download_fails() throws Exception {
    environmentVariables.set("_EXPERIMENTAL_DAGGER_CLI_BIN", null);
    CLIDownloader downloader = mock(CLIDownloader.class);
    when(downloader.downloadCLI()).thenThrow(new IOException("DOWNLOAD FAILED"));
    CLIRunner runner = new CLIRunner(".", false, downloader);
    assertThatThrownBy(() -> Connection.fromCLI(runner))
        .isInstanceOf(IOException.class)
        .hasMessage("DOWNLOAD FAILED");
  }

  @Test
  public void should_fail_when_clirunner_fails() throws Exception {
    CLIRunner runner = mock(CLIRunner.class);
    when(runner.getConnectionParams()).thenThrow(new IOException("FAILED"));
    assertThatThrownBy(() -> Connection.fromCLI(runner))
        .isInstanceOf(IOException.class)
        .hasMessage("FAILED");
  }
}
