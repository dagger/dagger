import { connect } from "@dagger.io/dagger"
import fetch from "node-fetch"

// initialize Dagger client
connect(
  async (client) => {
    // get secret from Vault
    const secretPlaintext = await getVaultSecret(
      "MOUNT-PATH",
      "SECRET-ID",
      "SECRET-KEY",
    )

    // load secret into Dagger
    const secret = client.setSecret("ghApiToken", secretPlaintext)

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

async function getVaultSecret(mountPath, secretID, secretKey) {
  // check for required variables in host environment
  const vars = [
    "VAULT_ADDRESS",
    "VAULT_NAMESPACE",
    "VAULT_ROLE_ID",
    "VAULT_SECRET_ID",
  ]
  vars.forEach((v) => {
    if (!process.env[v]) {
      console.log(`${v} variable must be set`)
      process.exit()
    }
  })

  const address = process.env.VAULT_ADDRESS
  const namespace = process.env.VAULT_NAMESPACE
  const role = process.env.VAULT_ROLE_ID
  const secret = process.env.VAULT_SECRET_ID

  // request client token
  let url = `${address}/v1/auth/approle/login`
  let body = { role_id: role, secret_id: secret }
  let options = {
    method: "POST",
    headers: {
      Accept: "application/json",
      "X-Vault-Namespace": `${namespace}`,
    },
    body: JSON.stringify(body),
  }

  // read client token
  let tokenResponse = await fetch(url, options)
    .then((res) => res.json())
    .catch((err) => console.error("Error: " + err))
  const token = tokenResponse.auth.client_token

  // request secret
  url = `${address}/v1/${mountPath}/data/${secretID}`
  options = {
    method: "GET",
    headers: {
      Accept: "application/json",
      "X-Vault-Namespace": `${namespace}`,
      "X-Vault-Token": `${token}`,
    },
  }

  // return secret
  let secretResponse = await fetch(url, options)
    .then((res) => res.json())
    .catch((err) => console.error("Error: " + err))

  return secretResponse.data.data[secretKey]
}
