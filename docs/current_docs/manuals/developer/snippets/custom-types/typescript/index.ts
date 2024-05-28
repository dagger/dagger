import { dag, object, field, func, GitRepository } from "@dagger.io/dagger"

@object()
class Account {
  @field()
  username: string

  @field()
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
  @field()
  url: string

  @field()
  repositories: GitRepository[]

  @field()
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
