import { connect } from "@dagger.io/dagger"

// check for required variables in host environment
const vars = [
  "AWS_ACCESS_KEY_ID",
  "AWS_SECRET_ACCESS_KEY",
  "AWS_DEFAULT_REGION",
]
vars.forEach((v) => {
  if (!process.env[v]) {
    console.log(`${v} variable must be set`)
    process.exit()
  }
})

// initialize Dagger client
connect(
  async (client) => {
    // set AWS credentials as client secrets
    let awsAccessKeyId = client.setSecret(
      "awsAccessKeyId",
      process.env["AWS_ACCESS_KEY_ID"],
    )
    let awsSecretAccessKey = client.setSecret(
      "awsSecretAccessKey",
      process.env["AWS_SECRET_ACCESS_KEY"],
    )

    let awsRegion = process.env["AWS_DEFAULT_REGION"]

    // get reference to function directory
    let lambdaDir = client
      .host()
      .directory(".", { exclude: ["ci", "node_modules"] })

    // use a node:18-alpine container
    // mount the function directory
    // at /src in the container
    // install application dependencies
    // create zip archive
    let build = client
      .container()
      .from("node:18-alpine")
      .withDirectory("/src", lambdaDir)
      .withWorkdir("/src")
      .withExec(["apk", "add", "zip"])
      .withExec(["npm", "install"])
      .withExec(["zip", "-r", "function.zip", "."])

    // use an AWS CLI container
    // set AWS credentials and configuration
    // as container environment variables
    let aws = client
      .container()
      .from("amazon/aws-cli:2.11.22")
      .withSecretVariable("AWS_ACCESS_KEY_ID", awsAccessKeyId)
      .withSecretVariable("AWS_SECRET_ACCESS_KEY", awsSecretAccessKey)
      .withEnvVariable("AWS_DEFAULT_REGION", awsRegion)

    // add zip archive to AWS CLI container
    // use CLI commands to deploy new zip archive
    // and get function URL
    // parse response and print URL
    let response = await aws
      .withFile("/tmp/function.zip", build.file("/src/function.zip"))
      .withExec([
        "lambda",
        "update-function-code",
        "--function-name",
        "myFunction",
        "--zip-file",
        "fileb:///tmp/function.zip",
      ])
      .withExec([
        "lambda",
        "get-function-url-config",
        "--function-name",
        "myFunction",
      ])
      .stdout()
    let url = JSON.parse(response).FunctionUrl
    console.log(`Function updated at: ${url}`)
  },
  { LogOutput: process.stderr },
)
