import { dag, object, func, GitRepository, field } from '@dagger.io/dagger';

@object()
class GitHubAccount {
  @field()
  username: string;

  @field()
  email: string;

  @field()
  url: string

  constructor(username: string, email: string) {
    this.username = username;
    this.email = email;
    this.url = `https://github.com/${username}`
  }
}

@object()
class GitHubOrganization {
  @field()
  url: string;

  @field()
  repository: GitRepository[];

  @field()
  members: GitHubAccount[];
}

@object()
class HelloWorld {
  @func()
  daggerOrganization(): GitHubOrganization {
    const organisation = new GitHubOrganization();

    organisation.url = 'https://github.com/dagger';
    organisation.repository = [dag.git(`${organisation.url}/dagger`)];
    organisation.members = [
      new GitHubAccount('jane', 'jane@example.com'),
      new GitHubAccount('john', 'john@example.com')
    ];

    return organisation;
  }
}
