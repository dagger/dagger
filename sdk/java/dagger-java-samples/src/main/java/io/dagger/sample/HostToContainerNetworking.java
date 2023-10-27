package io.dagger.sample;

import io.dagger.client.Client;
import io.dagger.client.Dagger;
import io.dagger.client.Service;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.List;

@Description("Expose a service from a container to the host")
public class HostToContainerNetworking {

  public static void main(String... args) throws Exception {
    try (Client client = Dagger.connect()) {
      // create web service container with exposed port 8080
      Service httpSrv =
          client
              .container()
              .from("python")
              .withDirectory("/srv", client.directory().withNewFile("index.html", "Hello, world!"))
              .withWorkdir("/srv")
              .withExec(List.of("python", "-m", "http.server", "8080"))
              .withExposedPort(8080)
              .asService();

      // expose web service to host
      Service tunnel = null;
      try {
        tunnel = client.host().tunnel(httpSrv).start();
        // get web service address
        String srvAddr = tunnel.endpoint();
        // access web service from host
        URL url = new URL("http://" + srvAddr);
        String body = new String(url.openStream().readAllBytes(), StandardCharsets.UTF_8);
        // print response
        System.out.println(body);
      } finally {
        tunnel.stop();
      }
    } catch (Exception e) {
      e.printStackTrace();
    }
  }
}
