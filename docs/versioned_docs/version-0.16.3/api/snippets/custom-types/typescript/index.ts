import { dag, object, func, GitRepository } from "@dagger.io/dagger"

@object()
class Account {
  @func()
  username: string

  @func()
  email: string

  constructor(username: string, email: string) {
    this.username = username
    this.email = email
  }

  @func()
  url(): string {
    return `https://github.com/${this.username}`
  }
}

/**
 * Organization has no specific methods, only exposed fields so
 * we can define it with `type` instead of `class` to
 * avoid the boilerplate of defining a constructor.
 */
export type Organization = {
  url: string
  repositories: GitRepository[]
  members: Account[]
}

@object()
class Github {
  @func()
  daggerOrganization(): Organization {
    const url = "https://github.com/dagger"

    const organization: Organization = {
      url,
      repositories: [dag.git(`${url}/dagger`)],
      members: [
        new Account("jane", "jane@example.com"),
        new Account("john", "john@example.com"),
      ],
    }

    return organization
  }
}
