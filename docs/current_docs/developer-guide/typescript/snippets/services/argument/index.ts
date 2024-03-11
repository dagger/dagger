import { dag, object, func, Service } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Sends a query to a MariaDB service received as input and returns the response
   */
  @func()
  async userList(hostService: Service): Promise<string> {
    return await dag
      .container()
      .from("mariadb:10.11.2")
      .withServiceBinding("db", hostService)
      .withExec([
        "/bin/sh",
        "-c",
        "/usr/bin/mysql --user=root --password=secret --host=db -e 'SELECT Host, User FROM mysql.user'",
      ])
      .stdout()
  }
}
