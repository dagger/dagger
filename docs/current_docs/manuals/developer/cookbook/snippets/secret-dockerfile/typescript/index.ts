import { dag, object, func, Secret } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  async githubApi(dir: Directory, secret: Secret): Promise<Container> {
    const secretName = await secret.name()
    return dir
          .dockerBuild({
            dockerfile: "Dockerfile",
            buildArgs: [
              {name: "gh-secret", value: secretName}
            ],
            secrets: [secret],
          })
  }
}
