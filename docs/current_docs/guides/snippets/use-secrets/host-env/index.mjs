import { connect } from "@dagger.io/dagger"

// check for required environment variable
if (!process.env["GH_SECRET"]) {
  console.log(`GH_SECRET variable must be set`)
  process.exit()
}

// initialize Dagger client
connect(
  async (client) => {
    // read secret from host variable
    const secret = client.setSecret("gh-secret", process.env["GH_SECRET"])

    // use secret in container environment
    const out = await client
      .container()
      .from("alpine:3.17")
      .withSecretVariable("GITHUB_API_TOKEN", secret)
      .withExec(["apk", "add", "curl"])
      .withExec([
        "sh",
        "-c",
        `curl "https://api.github.com/repos/dagger/dagger/issues" --header "Accept: application/vnd.github+json" --header "Authorization: Bearer $GITHUB_API_TOKEN"`,
      ])
      .stdout()

    // print result
    console.log(out)
  },
  { LogOutput: process.stderr },
)
