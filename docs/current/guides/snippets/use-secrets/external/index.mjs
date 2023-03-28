import { connect } from "@dagger.io/dagger"

import { SecretManagerServiceClient } from "@google-cloud/secret-manager"

// initialize Dagger client
connect(async (client) => {client

  // get secret from Google Cloud Secret Manager
  const secretPlaintext = await gcpGetSecretPlaintext("PROJECT-ID", "SECRET-ID")

  // load secret into Dagger
  const secret = client.setSecret("GH_SECRET", secretPlaintext)

  // use secret in container environment
  const out = await client.
    container().
    from("alpine:3.17").
    withSecretVariable("GITHUB_API_TOKEN", secret).
    withExec(["apk", "add", "curl"]).
    withExec(["sh", "-c", `curl "https://api.github.com/repos/dagger/dagger/issues" --header "Accept: application/vnd.github+json" --header "Authorization: Bearer $GITHUB_API_TOKEN"`]).
    stdout()

  // print result
  console.log(out)
}, {LogOutput: process.stderr})

async function gcpGetSecretPlaintext(projectID, secretID) {

  const client = new SecretManagerServiceClient();

  const secretVersion = `projects/${projectID}/secrets/${secretID}/versions/1`

  const [accessResponse] = await client.accessSecretVersion({
    name: secretVersion,
  });

  const secretPlaintext = accessResponse.payload.data.toString('utf8');
  return secretPlaintext;
}
