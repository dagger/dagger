import {
  dag,
  Container,
  Directory,
  Secret,
  object,
  func,
} from "@dagger.io/dagger";

@object()
class MyModule {
  /*
   * Return container image with application source code and dependencies
   */
  @func()
  build(source: Directory): Container {
    return dag
      .container()
      .from("php:8.2-apache-buster")
      .withExec(["apt-get", "update"])
      .withExec(["apt-get", "install", "--yes", "git-core", "zip", "curl"])
      .withExec(["docker-php-ext-install", "pdo", "pdo_mysql", "mysqli"])
      .withExec([
        "sh",
        "-c",
        "sed -ri -e 's!/var/www/html!/var/www/public!g' /etc/apache2/sites-available/*.conf",
      ])
      .withExec([
        "sh",
        "-c",
        "sed -ri -e 's!/var/www/!/var/www/public!g' /etc/apache2/apache2.conf /etc/apache2/conf-available/*.conf",
      ])
      .withExec(["a2enmod", "rewrite"])
      .withDirectory("/var/www", source.withoutDirectory("dagger"), {
        owner: "www-data",
      })
      .withWorkdir("/var/www")
      .withExec(["chmod", "-R", "775", "/var/www"])
      .withExec([
        "sh",
        "-c",
        "curl -sS https://getcomposer.org/installer | php -- --install-dir=/usr/local/bin --filename=composer",
      ])
      .withMountedCache(
        "/root/.composer/cache",
        dag.cacheVolume("composer-cache"),
      )
      .withMountedCache(
        "/var/www/vendor",
        dag.cacheVolume("composer-vendor-cache"),
      )
      .withExec(["composer", "install"]);
  }

  /*
   * Return result of unit tests
   */
  @func()
  async test(source: Directory): Promise<string> {
    return await this.build(source)
      .withEnvVariable("PATH", "./vendor/bin:$PATH", { expand: true })
      .withExec(["phpunit"])
      .stdout();
  }

  /*
   * Return address of published container image
   */
  @func()
  async publish(
    source: Directory,
    version: string,
    registryAddress: string,
    registryUsername: string,
    registryPassword: Secret,
    imageName: string,
  ): Promise<string> {
    const image = this.build(source)
      .withLabel("org.opencontainers.image.title", "Laravel with Dagger")
      .withLabel("org.opencontainers.image.version", version);
    // uncomment this to use a custom entrypoint file
    // .withExec(["chmod", "+x", "/var/www/docker-entrypoint.sh"])
    // .withEntrypoint(["/var/www/docker-entrypoint.sh"])

    const address = await image
      .withRegistryAuth(registryAddress, registryUsername, registryPassword)
      .publish(`${registryAddress}/${registryUsername}/${imageName}`);

    return address;
  }
}
