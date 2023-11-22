package io.dagger.sample;

import io.dagger.client.Client;
import io.dagger.client.Dagger;
import io.dagger.client.PortForward;
import io.dagger.client.Service;
import java.util.List;

@Description("Expose MySQL service running on the host to client containers")
public class ContainerToHostNetworking {

  public static void main(String... args) throws Exception {
    try (Client client = Dagger.connect()) {
      // expose host service on port 3306
      Service hostSrv =
          client.host().service(List.of(new PortForward().withBackend(3306).withFrontend(3306)));

      // create MariaDB container
      // with host service binding
      // execute SQL query on host service
      String out =
          client
              .container()
              .from("mariadb:10.11.2")
              .withServiceBinding("db", hostSrv)
              .withExec(
                  List.of(
                      "/bin/sh",
                      "-c",
                      "/usr/bin/mysql --user=root --host=db -e 'SELECT * FROM mysql.user'"))
              .stdout();
      System.out.println(out);
    } catch (Exception e) {
      e.printStackTrace();
    }
  }
}
