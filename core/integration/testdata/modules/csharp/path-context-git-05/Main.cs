// C# case of the cross-SDK ModuleSuite/TestContextGit matrix
// (core/integration/module_path_inputs_test.go): GitRepository and GitRef
// arguments resolved from [DefaultPath] context values.
//

using Dagger;

[Object]
public class Test
{
    [Function]
    public Task<string> TestRepoLocal([DefaultPath("./.git")] GitRepository git)
        => CommitAndRef(git.Head());

    [Function]
    public Task<string> TestRepoLocalAbs([DefaultPath("/")] GitRepository git)
        => CommitAndRef(git.Head());

    [Function]
    public Task<string> TestRepoRemote(
        [DefaultPath("https://github.com/dagger/dagger.git")] GitRepository git)
        => CommitAndRef(git.Tag("v0.18.2"));

    [Function]
    public Task<string> TestRefLocal([DefaultPath("./.git")] GitRef git)
        => CommitAndRef(git);

    [Function]
    public Task<string> TestRefRemote(
        [DefaultPath("https://github.com/dagger/dagger.git#v0.18.3")] GitRef git)
        => CommitAndRef(git);

    private static async Task<string> CommitAndRef(GitRef git)
    {
        var commit = await git.Commit();
        var reference = await git.Ref();
        return $"{reference}@{commit}";
    }
}
