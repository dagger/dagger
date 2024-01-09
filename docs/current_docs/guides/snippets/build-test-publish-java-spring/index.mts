import { connect, Client } from "@dagger.io/dagger"

// check for Docker Hub registry credentials in host environment
const vars = ["DOCKERHUB_USERNAME", "DOCKERHUB_PASSWORD"]
vars.forEach((v) => {
  if (!process.env[v]) {
    console.log(`${v} variable must be set`)
    process.exit()
  }
})

connect(
  async (client: Client) => {
    const username = process.env.DOCKERHUB_USERNAME
    // set registry password as secret for Dagger pipeline
    const password = client.setSecret(
      "password",
      process.env.DOCKERHUB_PASSWORD
    )

    // create a cache volume for Maven downloads
    const mavenCache = client.cacheVolume("maven-cache")

    // get reference to source code directory
    const source = client.host().directory(".", { exclude: ["ci/"] })

    // create database service container
    const mariadb = client
      .container()
      .from("mariadb:10.11.2")
      .withEnvVariable("MARIADB_USER", "petclinic")
      .withEnvVariable("MARIADB_PASSWORD", "petclinic")
      .withEnvVariable("MARIADB_DATABASE", "petclinic")
      .withEnvVariable("MARIADB_ROOT_PASSWORD", "root")
      .withExposedPort(3306)
      .asService()

    // use maven:3.9 container
    // mount cache and source code volumes
    // set working directory
    const app = client
      .container()
      .from("maven:3.9-eclipse-temurin-17")
      .withMountedCache("/root/.m2", mavenCache)
      .withMountedDirectory("/app", source)
      .withWorkdir("/app")

    // define binding between
    // application and service containers
    // define JDBC URL for tests
    // test, build and package application as JAR
    const build = app
      .withServiceBinding("db", mariadb)
      .withEnvVariable(
        "MYSQL_URL",
        "jdbc:mysql://petclinic:petclinic@db/petclinic"
      )
      .withExec(["mvn", "-Dspring.profiles.active=mysql", "clean", "package"])

    // use eclipse alpine container as base
    // copy JAR files from builder
    // set entrypoint and database profile
    const deploy = client
      .container()
      .from("eclipse-temurin:17-alpine")
      .withDirectory("/app", build.directory("./target"))
      .withEntrypoint([
        "java",
        "-jar",
        "-Dspring.profiles.active=mysql",
        "/app/spring-petclinic-3.0.0-SNAPSHOT.jar",
      ])

    // publish image to registry
    const address = await deploy
      .withRegistryAuth("docker.io", username, password)
      .publish(`${username}/myapp`)

    // print image address
    console.log(`Image published at: ${address}`)
  },
  { LogOutput: process.stderr }
)
