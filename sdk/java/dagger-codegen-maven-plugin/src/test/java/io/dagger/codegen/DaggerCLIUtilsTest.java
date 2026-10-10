package io.dagger.codegen;

import static org.assertj.core.api.Assertions.assertThat;

import org.junit.jupiter.api.Test;

class DaggerCLIUtilsTest {

  @Test
  void passesQueryVariablesAsJson() {
    assertThat(DaggerCLIUtils.queryCommand("dagger", null))
        .containsExactly("dagger", "query", "-s", "-M");
    assertThat(DaggerCLIUtils.queryCommand("/bin/dagger", "{\"names\":[\"withGPU\"]}"))
        .containsExactly(
            "/bin/dagger", "query", "-s", "-M", "--var-json", "{\"names\":[\"withGPU\"]}");
  }
}
