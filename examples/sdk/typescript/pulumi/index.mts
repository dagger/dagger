import { Buffer } from "buffer";
import { connect, Client } from "@dagger.io/dagger"
import * as os from "node:os";

connect(async (client: Client) => {

  const pulumiAccessToken = client.setSecret("PULUMI_ACCESS_TOKEN", process.env.PULUMI_ACCESS_TOKEN)

  // Create a base container from which we'll build our 2 Pulumi programs:
  const pulumiContainer = await client.container()
    .from("pulumi/pulumi-nodejs:3.76.0")
    // Pre-install the AWS plugin for improved performance as its own layer
    // (otherwise it'll download every time we run our first Pulumi command):
    .withExec(["/bin/bash", "-c", "pulumi plugin install resource aws"])
    // Mount our local Pulumi creds and program:
    .withSecretVariable("PULUMI_ACCESS_TOKEN", pulumiAccessToken)
    .withDirectory("/pulumi/projects", client.host().directory("infra"))
    // Mount any needed AWS credentials and config context from the host machine
    // to the build container. In a more prod-like scenario, we would probably
    // mount specific secrets into the container, e.g. AWS_ACCESS_KEY_ID, etc.:
    .withEnvVariable("AWS_PROFILE", process.env.AWS_PROFILE)
    .withDirectory("/root/.aws", client.host().directory(`${os.homedir()}/.aws`));
  
  // Create the ECR registry:
  const ecrOutput = await pulumiContainer
    .withEnvVariable("CACHEBUSTER", Date.now().toString())
    .withExec(["/bin/bash", "-c", "cd ecr && npm i && pulumi stack select dev -c && pulumi up -y --skip-preview"]);

  // The URI contains an extra newline
  const repositoryUrl = await ecrOutput
    .withExec(["/bin/bash", "-c", "cd ecr && pulumi stack output repositoryUrl -s dev"])
    .stdout()
    .then(x => x.trim());

  // Parse the credentials we need to push our build artifact:
  const authorizationToken = await ecrOutput
    .withExec(["/bin/bash", "-c", "cd ecr && pulumi stack output authorizationToken"])
    .stdout();

  const authorizationTokenDecoded = Buffer.from(authorizationToken, 'base64').toString();

  const userName = authorizationTokenDecoded.split(":")[0];
  const password = client.setSecret("password", authorizationTokenDecoded.split(":")[1]);

  // Build a sample app and push it to our ECR repository:
  const nodeCache = client.cacheVolume("node");

  const sourceDir = client
    .git("https://github.com/dagger/hello-dagger.git")
    .commit("5343dfee12cfc59013a51886388a7cacee3f16b9")
    .tree();

  const source = client
    .container()
    .from("node:16")
    .withDirectory("/src", sourceDir)
    .withMountedCache("/src/node_modules", nodeCache);

  const runner = source
    .withWorkdir("/src")
    .withExec(["npm", "install"]);

  const test = runner
    .withExec(["npm", "test", "--", "--watchAll=false"]);

  const buildDir = test
    .withExec(["npm", "run", "build"])
    .directory("./build");

  const containerRef = await client
    .container({platform: "linux/amd64"})
    .from("nginx")
    .withDirectory("/usr/share/nginx/html", buildDir)
    .withRegistryAuth(repositoryUrl.split("/")[0], userName, password)
    .publish(`${repositoryUrl}:latest`);

  console.log(`Successfully built and pushed ${containerRef}`)

  // Run our second Pulumi program deploys our build artifact as an ECS on
  // Fargate service:
  const ecsOutput = await pulumiContainer
    .withEnvVariable("CACHEBUSTER", Date.now().toString())
    .withExec(["/bin/bash", "-c", `cd ecs && npm i && pulumi stack select dev -c && pulumi up -y --skip-preview -c dagger-pulumi-demo-ecs:imageName=${containerRef}`]);

  const serviceUrl = await ecsOutput
    .withExec(["/bin/bash", "-c", "cd ecs && pulumi stack output serviceUrl -s dev"])
    .stdout()
    .then(x => x.trim());

  console.log(`serviceUrl: ${serviceUrl}`);

}, { LogOutput: process.stdout })