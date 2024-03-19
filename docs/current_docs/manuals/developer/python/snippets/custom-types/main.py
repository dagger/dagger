import dagger
from dagger import dag, field, function, object_type


@object_type
class Account:
    username: str = field()
    email: str = field()
    url: str = field(init=False)

    def __post_init__(self):
        self.url = f"https://github.com/{self.username}"


@object_type
class Organization:
    name: str = field()
    url: str = field()
    repositories: list[dagger.GitRepository] = field(default=list)
    members: list[Account] = field(default=list)

    @classmethod
    def create(cls, name: str, repositories: list[str], members: list[Account]):
        url = f"https://github.com/{name}"
        return cls(
            name=name,
            url=url,
            repositories=[dag.git(f"{url}/{repo}") for repo in repositories],
            members=members,
        )


@object_type
class Github:
    @function
    def dagger_organization(self) -> Organization:
        return Organization.create(
            name="dagger",
            repositories=["dagger"],
            members=[
                Account(username="jane", email="jane@example.com"),
                Account(username="john", email="john@example.com"),
            ],
        )
