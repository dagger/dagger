package io.dagger.client;

import static org.assertj.core.api.Assertions.*;
import static org.junit.jupiter.api.Assertions.*;

import java.util.List;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;

public class ClientIT {

  @BeforeAll
  public static void setLoggerConfig() {
    System.setProperty("org.slf4j.simpleLogger.log.io.dagger", "debug");
    System.setProperty("org.slf4j.simpleLogger.showShortLogName", "true");
  }

  @Test
  public void testDirectory() throws Exception {
    try (Client client = Dagger.connect()) {
      Directory dir = client.directory();
      String content = dir.withNewFile("/hello.txt", "world").file("/hello.txt").contents();
      assertEquals("world", content);
    }
  }

  @Test
  public void testGit() throws Exception {
    try (Client client = Dagger.connect()) {
      Directory tree = client.git("github.com/dagger/dagger").branch("main").tree();
      List<String> files = tree.entries();
      assertTrue(files.contains("README.md"));

      File readmeFile = tree.file("README.md");

      String readme = readmeFile.contents();
      assertFalse(readme.isEmpty());
      assertTrue(readme.contains("Dagger"));

      FileID readmeID = readmeFile.id();
      String otherReadme = client.loadFileFromID(readmeID).contents();
      assertEquals(readme, otherReadme);
    }
  }

  @Test
  public void testContainer() throws Exception {
    try (Client client = Dagger.connect()) {
      Container alpine = client.container().from("alpine:3.16.2");
      String contents = alpine.rootfs().file("/etc/alpine-release").contents();
      assertEquals("3.16.2\n", contents);

      String stdout = alpine.withExec(List.of("cat", "/etc/alpine-release")).stdout();
      assertEquals("3.16.2\n", stdout);

      // Ensure we can grab the container ID back and re-run the same query
      ContainerID id = alpine.id();
      contents = client.loadContainerFromID(id).rootfs().file("/etc/alpine-release").contents();
      assertEquals("3.16.2\n", contents);
    }
  }

  @Test
  public void testError() throws Exception {
    try (Client client = Dagger.connect()) {
      try {
        client.container().from("fake.invalid:latest").id();
      } catch (DaggerQueryException dqe) {
        assertThat(dqe.getErrors()).hasSizeGreaterThan(0);
      }

      try {
        client.container().from("alpine:3.16.2").withExec(List.of("false")).sync();
      } catch (DaggerQueryException dqe) {
        assertThat(dqe.getErrors()).hasSizeGreaterThan(0);
        assertThat(dqe.getErrors()[0].getExtensions()).containsEntry("_type", "EXEC_ERROR");
      }
    }
  }

  @Test
  public void testList() throws Exception {
    try (Client client = Dagger.connect()) {
      List<EnvVariable> envs =
          client
              .container()
              .from("alpine:3.16.2")
              .withEnvVariable("FOO", "BAR")
              .withEnvVariable("BAR", "BAZ")
              .envVariables();

      assertThat(envs).hasSizeGreaterThanOrEqualTo(3);

      String envName = envs.get(1).name();
      String envValue = envs.get(1).value();
      assertThat(envName).isEqualTo("FOO");
      assertThat(envValue).isEqualTo("BAR");

      envName = envs.get(2).name();
      envValue = envs.get(2).value();
      assertThat(envName).isEqualTo("BAR");
      assertThat(envValue).isEqualTo("BAZ");
    }
  }
}
