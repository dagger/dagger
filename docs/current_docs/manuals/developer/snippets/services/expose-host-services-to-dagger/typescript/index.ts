import { dag, object, func, Service } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Send a query to a MariaDB service and returns the response
   */
  @func()
  async userList(
    /**
     * Host service
     */
    svc: Service
  ): Promise<string> {
    return await dag
      .container()
      .from("mariadb:10.11.2")
      .withServiceBinding("db", svc)
      .withExec([
        "/usr/bin/mysql",
        "--user=root",
        "--password=secret",
        "--host=db",
        "-e",
        "SELECT Host, User FROM mysql.user",
      ])
      .stdout()
  }
}
