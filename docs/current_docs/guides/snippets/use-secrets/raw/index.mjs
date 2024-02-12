import { connect } from "@dagger.io/dagger"

// initialize Dagger client
connect(
  async (client) => {
    // set secret
    const secret = client.setSecret("ghApiToken", "TOKEN")

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
