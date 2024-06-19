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

@object()
class Organization {
  @func()
  url: string

  @func()
  repositories: GitRepository[]

  @func()
  members: Account[]
}

@object()
class Github {
  @func()
  daggerOrganization(): Organization {
    const organization = new Organization()

    organization.url = "https://github.com/dagger"
    organization.repositories = [dag.git(`${organization.url}/dagger`)]
    organization.members = [
      new Account("jane", "jane@example.com"),
      new Account("john", "john@example.com"),
    ]

    return organization
  }
}
