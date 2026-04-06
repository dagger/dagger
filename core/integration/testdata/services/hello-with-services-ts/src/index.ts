import { Service, dag, object, func, up } from "@dagger.io/dagger";

@object()
class HelloWithServicesTs {
  /**
   * Returns a web server service
   */
  @func()
  @up()
  web(): Service {
    return dag.container().from("nginx:alpine").withExposedPort(80).asService();
  }

  /**
   * Returns a redis service
   */
  @func()
  @up()
  redis(): Service {
    return dag
      .container()
      .from("redis:alpine")
      .withExposedPort(6379)
      .asService();
  }

  @func()
  infra(): Infra {
    return new Infra();
  }
}

@object()
class Infra {
  @func()
  @up()
  database(): Service {
    return dag
      .container()
      .from("postgres:alpine")
      .withEnvVariable("POSTGRES_PASSWORD", "test")
      .withExposedPort(5432)
      .asService();
  }
}
