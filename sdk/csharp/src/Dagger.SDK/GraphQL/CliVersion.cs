namespace Dagger.GraphQL;

/// <summary>
/// The Dagger CLI release this SDK is built for. The client downloads this release when it has
/// to start a session itself, and announces it to the engine to get the matching API.
/// Keep in sync with the engine version on release, like <c>sdk/go/engineconn/version.gen.go</c>.
/// </summary>
internal static class CliVersion
{
    internal const string Value = "1.0.0-beta.28";
}
