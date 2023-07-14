# frozen_string_literal: true

require_relative "lib/dagger"

client = Dagger.connect!

puts client
  .container
  .from("alpine")
  .with_exec("apk", "add", "curl")
  .with_exec("curl", "--version")
  .stdout

sources = client.host.directory("./demo", exclude: ["node_modules/", "ci/"])

puts client
  .container
  .from("node:16-slim")
  .with_directory("/src", sources)
  .with_workdir("/src")
  .with_exec("npm", "install")
  .stderr
