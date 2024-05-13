import dagger
from dagger import dag, field, function, object_type


@object_type
class Account:
    username: str = field()
    email: str = field()

    @function
    def url(self) -> str:
        return f"https://github.com/{self.username}"


@object_type
class Organization:
    url: str = field()
    repositories: list[dagger.GitRepository] = field()
    members: list[Account] = field()


@object_type
class Github:
    @function
    def dagger_organization(self) -> Organization:
        url = "https://github.com/dagger"
        return Organization(
            url=url,
            repositories=[dag.git(f"{url}/dagger")],
            members=[
                Account(username="jane", email="jane@example.com"),
                Account(username="john", email="john@example.com"),
            ],
        )
