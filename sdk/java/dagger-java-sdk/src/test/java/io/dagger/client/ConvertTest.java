package io.dagger.client;

import org.junit.jupiter.api.Test;

import static org.assertj.core.api.Assertions.assertThat;

public class ConvertTest {
  @Test
  public void json_convert() throws Exception {
    try (Client client = Dagger.connect()) {
      Container alpine = client.container().from("alpine:3.16.2").withWorkdir("/my-dir");
      assertThat(alpine.workdir()).isEqualTo("/my-dir");

      JSON json = Convert.toJSON(alpine);

      Container a = Convert.fromJSON(client, json, Container.class);
      assertThat(a.workdir()).isEqualTo("/my-dir");
    }
  }
}
