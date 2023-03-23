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
  .withServiceBinding("db", database) // bind database with the name db
  .withEnvVariable("DB_HOST", "db") // db refers to the service binding
  .withEnvVariable("DB_PASSWORD", "test") // password set in db container
  .withEnvVariable("DB_USER", "postgres") // default user in postgres image
  .withEnvVariable("DB_NAME", "postgres") // default db name in postgres image
  .withMountedDirectory("/src", source)
  .withWorkdir("/src")
  .withExec(["npm", "install"])
  .withExec(["npm", "run", "test"]) // execute npm run test
  .stdout()

}, {LogOutput: process.stdout})
