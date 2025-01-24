package io.dagger.client;

import static org.assertj.core.api.Assertions.assertThat;

import org.junit.jupiter.api.Test;

public class ConvertTest {
  @Test
  public void json_convert() throws Exception {
    try (Client client = Dagger.connect()) {
      Container alpine = client.container().from("alpine:3.16.2").withWorkdir("/my-dir");
      assertThat(alpine.workdir()).isEqualTo("/my-dir");

      JSON json = JsonConverter.toJSON(alpine);

      Container a = JsonConverter.fromJSON(client, json, Container.class);
      assertThat(a.workdir()).isEqualTo("/my-dir");
    }
  }
}
