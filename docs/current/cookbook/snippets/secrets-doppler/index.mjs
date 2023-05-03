import { connect } from "@dagger.io/dagger"

import fetch from "node-fetch"

// initialize Dagger client
connect(async (client) => {

  // get secret from Doppler
  const secretPlaintext = await getDopplerSecret("PROJECT-ID", "CONFIG-ID", "SECRET-ID")

  // load secret into Dagger
  const secret = client.setSecret("ghApiToken", secretPlaintext)

  // use secret in container environment
  const out = await client
    .container()
    .from("alpine:3.17")
    .withSecretVariable("GITHUB_API_TOKEN", secret)
    .withExec(["apk", "add", "curl"])
    .withExec(["sh", "-c", `curl "https://api.github.com/repos/dagger/dagger/issues" --header "Accept: application/vnd.github+json" --header "Authorization: Bearer $GITHUB_API_TOKEN"`])
    .stdout()

  // print result
  console.log(out)
}, {LogOutput: process.stderr})

async function getDopplerSecret(projectID, configID, secretID) {

  // check for Doppler service token in host environment
  if(!process.env.DOPPLER_TOKEN) { 
    console.log('DOPPLER_TOKEN environment variable must be set');
    process.exit(); 
  }

  // prepare Doppler API request
  const url = `https://api.doppler.com/v3/configs/config/secret?project=${projectID}&config=${configID}&name=${secretID}`;
  const options = {
    method: 'GET',
    headers: {
      accept: 'application/json',
      authorization: 'Bearer ' + process.env.DOPPLER_TOKEN
    }
  };

  // read API response
  const json = await fetch(url, options)
    .then(res => res.json())
    .catch(err => console.error('Error: ' + err));

  return json.value.raw
}