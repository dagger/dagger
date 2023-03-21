import { connect } from "@dagger.io/dagger"

// initialize Dagger client
connect(async (client) => {

  const database = client.container().from("postgres:15.2")
  .withEnvVariable("POSTGRES_PASSWORD", "test")
  .withExec(["postgres"])
  .withExposedPort(5432)
  // get reference to the local project
  const source = client.host().directory(".", { exclude: ["node_modules/"]})

  await client.container().from("node:16")
  .withServiceBinding("db", database)
  .withEnvVariable("DB_HOST", "db")
  .withEnvVariable("DB_PASSWORD", "test")
  .withEnvVariable("DB_USER", "postgres")
  .withEnvVariable("DB_NAME", "postgres")
  .withMountedDirectory("/src", source)
  .withWorkdir("/src")
  .withExec(["npm", "install"])
  .withExec(["npm", "run", "test"])
  .stdout()

}, {LogOutput: process.stdout})
