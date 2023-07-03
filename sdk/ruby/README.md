# Dagger Ruby SDK

A client package for running [Dagger](https://dagger.io/) pipelines.

## What is the Dagger Ruby SDK?

The Dagger Ruby SDK contains everything you need to develop CI/CD pipelines in Ruby, and run them on any OCI-compatible container runtime.

## Example

Create a `main.rb` file:

```ruby
client = Dagger.connect!

sources = client.host.directory("./demo", exclude: ["node_modules/", "ci/"])

puts client
  .container
  .from("node:16-slim")
  .with_directory("/src", sources)
  .with_workdir("/src")
  .with_exec("npm", "install")
  .stderr
```
