from typing import Annotated
import dagger
from dagger import DefaultPath, function, object_type

@object_type
class Test:
	@function
	async def test_repo_local(self, git: Annotated[dagger.GitRepository, DefaultPath("./.git")]) -> str:
		return await self.commit_and_ref(git.head())

	@function
	async def test_repo_local_abs(self, git: Annotated[dagger.GitRepository, DefaultPath("/")]) -> str:
		return await self.commit_and_ref(git.head())

	@function
	async def test_repo_remote(self, git: Annotated[dagger.GitRepository, DefaultPath("https://github.com/dagger/dagger.git")]) -> str:
		return await self.commit_and_ref(git.tag("v0.18.2"))

	@function
	async def test_ref_local(self, git: Annotated[dagger.GitRef, DefaultPath("./.git")]) -> str:
		return await self.commit_and_ref(git)

	@function
	async def test_ref_remote(self, git: Annotated[dagger.GitRef, DefaultPath("https://github.com/dagger/dagger.git#v0.18.3")]) -> str:
		return await self.commit_and_ref(git)

	async def commit_and_ref(self, ref: dagger.GitRef) -> str:
		commit = await ref.commit()
		reference = await ref.ref()
		return f"{reference}@{commit}"
