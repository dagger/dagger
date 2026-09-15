import { GitRepository, GitRef, object, func, argument } from "@dagger.io/dagger"

@object()
export class Test {
	@func()
	async testRepoLocal(
		@argument({ defaultPath: "./.git" }) git: GitRepository,
	): Promise<string> {
		return await this.commitAndRef(git.head())
	}

	@func()
	async testRepoLocalAbs(
		@argument({ defaultPath: "/" }) git: GitRepository,
	): Promise<string> {
		return await this.commitAndRef(git.head())
	}

	@func()
	async testRepoRemote(
		@argument({ defaultPath: "https://github.com/dagger/dagger.git" }) git: GitRepository,
	): Promise<string> {
		return await this.commitAndRef(git.tag("v0.18.2"))
	}

	@func()
	async testRefLocal(
		@argument({ defaultPath: "./.git" }) git: GitRef,
	): Promise<string> {
		return await this.commitAndRef(git)
	}

	@func()
	async testRefRemote(
		@argument({ defaultPath: "https://github.com/dagger/dagger.git#v0.18.3" }) git: GitRef,
	): Promise<string> {
		return await this.commitAndRef(git)
	}

	async commitAndRef(git: GitRef): Promise<string> {
		const commit = await git.commit()
		const reference = await git.ref()
		return reference + "@" + commit
	}
}
