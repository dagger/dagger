/* 
    This file is auto-generated using the Dagger.SDk.CodeGen tool.
    This file is included for compilation purposes.

    When a module is initalized, the codegen tool generates the necessary 
    replacement of this file within the module's build context.
*/

#nullable enable
using System.Collections.Immutable;
using System.Text.Json.Serialization;
using System.Threading;
using System.Threading.Tasks;
using Dagger.GraphQL;
using Dagger.JsonConverters;

namespace Dagger;

/// <summary>
/// A standardized address to load containers, directories, secrets, and other object types. Address format depends on the type, and is validated at type selection.
/// </summary>
public class Address(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<AddressId>
{
    /// <summary>
    /// Load a container from the address.
    /// </summary>
    public Container Container()
    {
        var queryBuilder = QueryBuilder.Select("container");
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a directory from the address.
    /// </summary>
    /// <param name = "exclude">
    /// 
    /// </param>
    /// <param name = "include">
    /// 
    /// </param>
    /// <param name = "gitignore">
    /// 
    /// </param>
    /// <param name = "noCache">
    /// 
    /// </param>
    public Directory Directory(string[]? exclude = null, string[]? include = null, bool? gitignore = false, bool? noCache = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (exclude is string[] exclude_)
        {
            arguments = arguments.Add(new Argument("exclude", new ListValue(exclude_.Select(v => new StringValue(v) as Value).ToList())));
        }

        if (include is string[] include_)
        {
            arguments = arguments.Add(new Argument("include", new ListValue(include_.Select(v => new StringValue(v) as Value).ToList())));
        }

        if (gitignore is bool gitignore_)
        {
            arguments = arguments.Add(new Argument("gitignore", new BooleanValue(gitignore_)));
        }

        if (noCache is bool noCache_)
        {
            arguments = arguments.Add(new Argument("noCache", new BooleanValue(noCache_)));
        }

        var queryBuilder = QueryBuilder.Select("directory", arguments);
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a file from the address.
    /// </summary>
    /// <param name = "exclude">
    /// 
    /// </param>
    /// <param name = "include">
    /// 
    /// </param>
    /// <param name = "gitignore">
    /// 
    /// </param>
    /// <param name = "noCache">
    /// 
    /// </param>
    public File File(string[]? exclude = null, string[]? include = null, bool? gitignore = false, bool? noCache = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (exclude is string[] exclude_)
        {
            arguments = arguments.Add(new Argument("exclude", new ListValue(exclude_.Select(v => new StringValue(v) as Value).ToList())));
        }

        if (include is string[] include_)
        {
            arguments = arguments.Add(new Argument("include", new ListValue(include_.Select(v => new StringValue(v) as Value).ToList())));
        }

        if (gitignore is bool gitignore_)
        {
            arguments = arguments.Add(new Argument("gitignore", new BooleanValue(gitignore_)));
        }

        if (noCache is bool noCache_)
        {
            arguments = arguments.Add(new Argument("noCache", new BooleanValue(noCache_)));
        }

        var queryBuilder = QueryBuilder.Select("file", arguments);
        return new File(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a git ref (branch, tag or commit) from the address.
    /// </summary>
    public GitRef GitRef()
    {
        var queryBuilder = QueryBuilder.Select("gitRef");
        return new GitRef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a git repository from the address.
    /// </summary>
    public GitRepository GitRepository()
    {
        var queryBuilder = QueryBuilder.Select("gitRepository");
        return new GitRepository(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// A unique identifier for this Address.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<AddressId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<AddressId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Load a secret from the address.
    /// </summary>
    public Secret Secret()
    {
        var queryBuilder = QueryBuilder.Select("secret");
        return new Secret(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a service from the address.
    /// </summary>
    public Service Service()
    {
        var queryBuilder = QueryBuilder.Select("service");
        return new Service(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a local socket from the address.
    /// </summary>
    public Socket Socket()
    {
        var queryBuilder = QueryBuilder.Select("socket");
        return new Socket(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// The address value
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> ValueAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("value");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// The `AddressID` scalar type represents an identifier for an object of type Address.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<AddressId>))]
public class AddressId : Scalar
{
}

/// <summary>
/// Binding
/// </summary>
public class Binding(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<BindingId>
{
    /// <summary>
    /// Retrieve the binding value, as type Address
    /// </summary>
    public Address AsAddress()
    {
        var queryBuilder = QueryBuilder.Select("asAddress");
        return new Address(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieve the binding value, as type CacheVolume
    /// </summary>
    public CacheVolume AsCacheVolume()
    {
        var queryBuilder = QueryBuilder.Select("asCacheVolume");
        return new CacheVolume(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieve the binding value, as type Changeset
    /// </summary>
    public Changeset AsChangeset()
    {
        var queryBuilder = QueryBuilder.Select("asChangeset");
        return new Changeset(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieve the binding value, as type Check
    /// </summary>
    public Check AsCheck()
    {
        var queryBuilder = QueryBuilder.Select("asCheck");
        return new Check(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieve the binding value, as type CheckGroup
    /// </summary>
    public CheckGroup AsCheckGroup()
    {
        var queryBuilder = QueryBuilder.Select("asCheckGroup");
        return new CheckGroup(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieve the binding value, as type Cloud
    /// </summary>
    public Cloud AsCloud()
    {
        var queryBuilder = QueryBuilder.Select("asCloud");
        return new Cloud(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieve the binding value, as type Container
    /// </summary>
    public Container AsContainer()
    {
        var queryBuilder = QueryBuilder.Select("asContainer");
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieve the binding value, as type Directory
    /// </summary>
    public Directory AsDirectory()
    {
        var queryBuilder = QueryBuilder.Select("asDirectory");
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieve the binding value, as type Env
    /// </summary>
    public Env AsEnv()
    {
        var queryBuilder = QueryBuilder.Select("asEnv");
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieve the binding value, as type EnvFile
    /// </summary>
    public EnvFile AsEnvFile()
    {
        var queryBuilder = QueryBuilder.Select("asEnvFile");
        return new EnvFile(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieve the binding value, as type File
    /// </summary>
    public File AsFile()
    {
        var queryBuilder = QueryBuilder.Select("asFile");
        return new File(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieve the binding value, as type GitRef
    /// </summary>
    public GitRef AsGitRef()
    {
        var queryBuilder = QueryBuilder.Select("asGitRef");
        return new GitRef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieve the binding value, as type GitRepository
    /// </summary>
    public GitRepository AsGitRepository()
    {
        var queryBuilder = QueryBuilder.Select("asGitRepository");
        return new GitRepository(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieve the binding value, as type JSONValue
    /// </summary>
    public Jsonvalue AsJsonvalue()
    {
        var queryBuilder = QueryBuilder.Select("asJSONValue");
        return new Jsonvalue(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieve the binding value, as type Module
    /// </summary>
    public Module AsModule()
    {
        var queryBuilder = QueryBuilder.Select("asModule");
        return new Module(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieve the binding value, as type ModuleConfigClient
    /// </summary>
    public ModuleConfigClient AsModuleConfigClient()
    {
        var queryBuilder = QueryBuilder.Select("asModuleConfigClient");
        return new ModuleConfigClient(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieve the binding value, as type ModuleSource
    /// </summary>
    public ModuleSource AsModuleSource()
    {
        var queryBuilder = QueryBuilder.Select("asModuleSource");
        return new ModuleSource(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieve the binding value, as type SearchResult
    /// </summary>
    public SearchResult AsSearchResult()
    {
        var queryBuilder = QueryBuilder.Select("asSearchResult");
        return new SearchResult(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieve the binding value, as type SearchSubmatch
    /// </summary>
    public SearchSubmatch AsSearchSubmatch()
    {
        var queryBuilder = QueryBuilder.Select("asSearchSubmatch");
        return new SearchSubmatch(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieve the binding value, as type Secret
    /// </summary>
    public Secret AsSecret()
    {
        var queryBuilder = QueryBuilder.Select("asSecret");
        return new Secret(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieve the binding value, as type Service
    /// </summary>
    public Service AsService()
    {
        var queryBuilder = QueryBuilder.Select("asService");
        return new Service(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieve the binding value, as type Socket
    /// </summary>
    public Socket AsSocket()
    {
        var queryBuilder = QueryBuilder.Select("asSocket");
        return new Socket(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Returns the binding's string value
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> AsStringAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("asString");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Returns the digest of the binding value
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> DigestAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("digest");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A unique identifier for this Binding.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<BindingId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<BindingId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Returns true if the binding is null
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<bool> IsNullAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("isNull");
        return await QueryExecutor.ExecuteAsync<bool>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Returns the binding name
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> NameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("name");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Returns the binding type
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> TypeNameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("typeName");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// The `BindingID` scalar type represents an identifier for an object of type Binding.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<BindingId>))]
public class BindingId : Scalar
{
}

/// <summary>
/// Key value object that represents a build argument.
/// </summary>
public struct BuildArg(string name, string value) : IInputObject
{
    /// <summary>
    /// The build argument name.
    /// </summary>
    public string Name { get; } = name;
    /// <summary>
    /// The build argument value.
    /// </summary>
    public string Value { get; } = value;

    /// <summary>
    /// Converts this input object to GraphQL key-value pairs.
    /// </summary>
    public List<KeyValuePair<string, Value>> ToKeyValuePairs()
    {
        var kvPairs = new List<KeyValuePair<string, Value>>();
        kvPairs.Add(new KeyValuePair<string, Value>("name", new StringValue(Name) as Value));
        kvPairs.Add(new KeyValuePair<string, Value>("value", new StringValue(Value) as Value));
        return kvPairs;
    }
}

/// <summary>
/// Sharing mode of the cache volume.
/// </summary>
[JsonConverter(typeof(JsonStringEnumConverter<CacheSharingMode>))]
public enum CacheSharingMode
{
    /// <summary>
    /// Shares the cache volume amongst many build pipelines
    /// </summary>
    SHARED,
    /// <summary>
    /// Keeps a cache volume for a single build pipeline
    /// </summary>
    PRIVATE,
    /// <summary>
    /// Shares the cache volume amongst many build pipelines, but will serialize the writes
    /// </summary>
    LOCKED
}

/// <summary>
/// A directory whose contents persist across runs.
/// </summary>
public class CacheVolume(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<CacheVolumeId>
{
    /// <summary>
    /// A unique identifier for this CacheVolume.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<CacheVolumeId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<CacheVolumeId>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// The `CacheVolumeID` scalar type represents an identifier for an object of type CacheVolume.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<CacheVolumeId>))]
public class CacheVolumeId : Scalar
{
}

/// <summary>
/// A comparison between two directories representing changes that can be applied.
/// </summary>
public class Changeset(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<ChangesetId>
{
    /// <summary>
    /// Files and directories that were added in the newer directory.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string[]> AddedPathsAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("addedPaths");
        return await QueryExecutor.ExecuteListAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The newer/upper snapshot.
    /// </summary>
    public Directory After()
    {
        var queryBuilder = QueryBuilder.Select("after");
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Return a Git-compatible patch of the changes
    /// </summary>
    public File AsPatch()
    {
        var queryBuilder = QueryBuilder.Select("asPatch");
        return new File(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// The older/lower snapshot to compare against.
    /// </summary>
    public Directory Before()
    {
        var queryBuilder = QueryBuilder.Select("before");
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Applies the diff represented by this changeset to a path on the host.
    /// </summary>
    /// <param name = "path">
    /// Location of the copied directory (e.g., "logs/").
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> ExportAsync(string path, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path)));
        var queryBuilder = QueryBuilder.Select("export", arguments);
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A unique identifier for this Changeset.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<ChangesetId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<ChangesetId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Returns true if the changeset is empty (i.e. there are no changes).
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<bool> IsEmptyAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("isEmpty");
        return await QueryExecutor.ExecuteAsync<bool>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Return a snapshot containing only the created and modified files
    /// </summary>
    public Directory Layer()
    {
        var queryBuilder = QueryBuilder.Select("layer");
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Files and directories that existed before and were updated in the newer directory.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string[]> ModifiedPathsAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("modifiedPaths");
        return await QueryExecutor.ExecuteListAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Files and directories that were removed. Directories are indicated by a trailing slash, and their child paths are not included.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string[]> RemovedPathsAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("removedPaths");
        return await QueryExecutor.ExecuteListAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Force evaluation in the engine.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<ChangesetId> SyncAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("sync");
        return await QueryExecutor.ExecuteAsync<ChangesetId>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// The `ChangesetID` scalar type represents an identifier for an object of type Changeset.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<ChangesetId>))]
public class ChangesetId : Scalar
{
}

/// <summary>
/// Check
/// </summary>
public class Check(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<CheckId>
{
    /// <summary>
    /// Whether the check completed
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<bool> CompletedAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("completed");
        return await QueryExecutor.ExecuteAsync<bool>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The description of the check
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> DescriptionAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("description");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A unique identifier for this Check.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<CheckId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<CheckId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Return the fully qualified name of the check
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> NameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("name");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Whether the check passed
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<bool> PassedAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("passed");
        return await QueryExecutor.ExecuteAsync<bool>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The path of the check within its module
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string[]> PathAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("path");
        return await QueryExecutor.ExecuteListAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// An emoji representing the result of the check
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> ResultEmojiAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("resultEmoji");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Execute the check
    /// </summary>
    public Check Run()
    {
        var queryBuilder = QueryBuilder.Select("run");
        return new Check(queryBuilder, GraphQLClient);
    }
}

/// <summary>
/// CheckGroup
/// </summary>
public class CheckGroup(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<CheckGroupId>
{
    /// <summary>
    /// A unique identifier for this CheckGroup.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<CheckGroupId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<CheckGroupId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Return a list of individual checks and their details
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Check[]> ListAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("list").Select("id");
        return (await QueryExecutor.ExecuteListAsync<CheckId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new Check(QueryBuilder.Builder().Select("loadCheckFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// Generate a markdown report
    /// </summary>
    public File Report()
    {
        var queryBuilder = QueryBuilder.Select("report");
        return new File(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Execute all selected checks
    /// </summary>
    public CheckGroup Run()
    {
        var queryBuilder = QueryBuilder.Select("run");
        return new CheckGroup(queryBuilder, GraphQLClient);
    }
}

/// <summary>
/// The `CheckGroupID` scalar type represents an identifier for an object of type CheckGroup.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<CheckGroupId>))]
public class CheckGroupId : Scalar
{
}

/// <summary>
/// The `CheckID` scalar type represents an identifier for an object of type Check.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<CheckId>))]
public class CheckId : Scalar
{
}

/// <summary>
/// Dagger Cloud configuration and state
/// </summary>
public class Cloud(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<CloudId>
{
    /// <summary>
    /// A unique identifier for this Cloud.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<CloudId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<CloudId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The trace URL for the current session
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> TraceUrlAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("traceURL");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// The `CloudID` scalar type represents an identifier for an object of type Cloud.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<CloudId>))]
public class CloudId : Scalar
{
}

/// <summary>
/// An OCI-compatible container, also known as a Docker container.
/// </summary>
public class Container(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<ContainerId>
{
    /// <summary>
    /// Turn the container into a Service.
    ///
    /// Be sure to set any exposed ports before this conversion.
    /// </summary>
    /// <param name = "args">
    /// Command to run instead of the container's default command (e.g., ["go", "run", "main.go"]).
    /// 
    /// If empty, the container's default command is used.
    /// </param>
    /// <param name = "useEntrypoint">
    /// If the container has an entrypoint, prepend it to the args.
    /// </param>
    /// <param name = "experimentalPrivilegedNesting">
    /// Provides Dagger access to the executed command.
    /// </param>
    /// <param name = "insecureRootCapabilities">
    /// Execute the command with all root capabilities. This is similar to running a command with "sudo" or executing "docker run" with the "--privileged" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.
    /// </param>
    /// <param name = "expand">
    /// Replace "${VAR}" or "$VAR" in the args according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    /// </param>
    /// <param name = "noInit">
    /// If set, skip the automatic init process injected into containers by default.
    /// 
    /// This should only be used if the user requires that their exec process be the pid 1 process in the container. Otherwise it may result in unexpected behavior.
    /// </param>
    public Service AsService(string[]? args = null, bool? useEntrypoint = false, bool? experimentalPrivilegedNesting = false, bool? insecureRootCapabilities = false, bool? expand = false, bool? noInit = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (args is string[] args_)
        {
            arguments = arguments.Add(new Argument("args", new ListValue(args_.Select(v => new StringValue(v) as Value).ToList())));
        }

        if (useEntrypoint is bool useEntrypoint_)
        {
            arguments = arguments.Add(new Argument("useEntrypoint", new BooleanValue(useEntrypoint_)));
        }

        if (experimentalPrivilegedNesting is bool experimentalPrivilegedNesting_)
        {
            arguments = arguments.Add(new Argument("experimentalPrivilegedNesting", new BooleanValue(experimentalPrivilegedNesting_)));
        }

        if (insecureRootCapabilities is bool insecureRootCapabilities_)
        {
            arguments = arguments.Add(new Argument("insecureRootCapabilities", new BooleanValue(insecureRootCapabilities_)));
        }

        if (expand is bool expand_)
        {
            arguments = arguments.Add(new Argument("expand", new BooleanValue(expand_)));
        }

        if (noInit is bool noInit_)
        {
            arguments = arguments.Add(new Argument("noInit", new BooleanValue(noInit_)));
        }

        var queryBuilder = QueryBuilder.Select("asService", arguments);
        return new Service(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Package the container state as an OCI image, and return it as a tar archive
    /// </summary>
    /// <param name = "platformVariants">
    /// Identifiers for other platform specific containers.
    /// 
    /// Used for multi-platform images.
    /// </param>
    /// <param name = "forcedCompression">
    /// Force each layer of the image to use the specified compression algorithm.
    /// 
    /// If this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.
    /// </param>
    /// <param name = "mediaTypes">
    /// Use the specified media types for the image's layers.
    /// 
    /// Defaults to OCI, which is largely compatible with most recent container runtimes, but Docker may be needed for older runtimes without OCI support.
    /// </param>
    public File AsTarball(Container[]? platformVariants = null, ImageLayerCompression? forcedCompression = null, ImageMediaTypes? mediaTypes = ImageMediaTypes.OCIMediaTypes)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (platformVariants is Container[] platformVariants_)
        {
            arguments = arguments.Add(new Argument("platformVariants", new ListValue(platformVariants_.Select(v => new IdValue<ContainerId>(v) as Value).ToList())));
        }

        if (forcedCompression is ImageLayerCompression forcedCompression_)
        {
            arguments = arguments.Add(new Argument("forcedCompression", new StringValue(forcedCompression_.ToString())));
        }

        if (mediaTypes is ImageMediaTypes mediaTypes_)
        {
            arguments = arguments.Add(new Argument("mediaTypes", new StringValue(mediaTypes_.ToString())));
        }

        var queryBuilder = QueryBuilder.Select("asTarball", arguments);
        return new File(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// The combined buffered standard output and standard error stream of the last executed command
    ///
    /// Returns an error if no command was executed
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> CombinedOutputAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("combinedOutput");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Return the container's default arguments.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string[]> DefaultArgsAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("defaultArgs");
        return await QueryExecutor.ExecuteListAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Retrieve a directory from the container's root filesystem
    ///
    /// Mounts are included.
    /// </summary>
    /// <param name = "path">
    /// The path of the directory to retrieve (e.g., "./src").
    /// </param>
    /// <param name = "expand">
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    /// </param>
    public Directory Directory(string path, bool? expand = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path)));
        if (expand is bool expand_)
        {
            arguments = arguments.Add(new Argument("expand", new BooleanValue(expand_)));
        }

        var queryBuilder = QueryBuilder.Select("directory", arguments);
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Return the container's OCI entrypoint.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string[]> EntrypointAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("entrypoint");
        return await QueryExecutor.ExecuteListAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Retrieves the value of the specified environment variable.
    /// </summary>
    /// <param name = "name">
    /// The name of the environment variable to retrieve (e.g., "PATH").
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> EnvVariableAsync(string name, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name)));
        var queryBuilder = QueryBuilder.Select("envVariable", arguments);
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Retrieves the list of environment variables passed to commands.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<EnvVariable[]> EnvVariablesAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("envVariables").Select("id");
        return (await QueryExecutor.ExecuteListAsync<EnvVariableId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new EnvVariable(QueryBuilder.Builder().Select("loadEnvVariableFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// check if a file or directory exists
    /// </summary>
    /// <param name = "path">
    /// Path to check (e.g., "/file.txt").
    /// </param>
    /// <param name = "expectedType">
    /// If specified, also validate the type of file (e.g. "REGULAR_TYPE", "DIRECTORY_TYPE", or "SYMLINK_TYPE").
    /// </param>
    /// <param name = "doNotFollowSymlinks">
    /// If specified, do not follow symlinks.
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<bool> ExistsAsync(string path, ExistsType? expectedType = null, bool? doNotFollowSymlinks = false, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path)));
        if (expectedType is ExistsType expectedType_)
        {
            arguments = arguments.Add(new Argument("expectedType", new StringValue(expectedType_.ToString())));
        }

        if (doNotFollowSymlinks is bool doNotFollowSymlinks_)
        {
            arguments = arguments.Add(new Argument("doNotFollowSymlinks", new BooleanValue(doNotFollowSymlinks_)));
        }

        var queryBuilder = QueryBuilder.Select("exists", arguments);
        return await QueryExecutor.ExecuteAsync<bool>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The exit code of the last executed command
    ///
    /// Returns an error if no command was executed
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<int> ExitCodeAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("exitCode");
        return await QueryExecutor.ExecuteAsync<int>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// EXPERIMENTAL API! Subject to change/removal at any time.
    ///
    /// Configures all available GPUs on the host to be accessible to this container.
    ///
    /// This currently works for Nvidia devices only.
    /// </summary>
    public Container ExperimentalWithAllGpus()
    {
        var queryBuilder = QueryBuilder.Select("experimentalWithAllGPUs");
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// EXPERIMENTAL API! Subject to change/removal at any time.
    ///
    /// Configures the provided list of devices to be accessible to this container.
    ///
    /// This currently works for Nvidia devices only.
    /// </summary>
    /// <param name = "devices">
    /// List of devices to be accessible to this container.
    /// </param>
    public Container ExperimentalWithGpu(string[] devices)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("devices", new ListValue(devices.Select(v => new StringValue(v) as Value).ToList())));
        var queryBuilder = QueryBuilder.Select("experimentalWithGPU", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Writes the container as an OCI tarball to the destination file path on the host.
    ///
    /// It can also export platform variants.
    /// </summary>
    /// <param name = "path">
    /// Host's destination path (e.g., "./tarball").
    /// 
    /// Path can be relative to the engine's workdir or absolute.
    /// </param>
    /// <param name = "platformVariants">
    /// Identifiers for other platform specific containers.
    /// 
    /// Used for multi-platform image.
    /// </param>
    /// <param name = "forcedCompression">
    /// Force each layer of the exported image to use the specified compression algorithm.
    /// 
    /// If this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.
    /// </param>
    /// <param name = "mediaTypes">
    /// Use the specified media types for the exported image's layers.
    /// 
    /// Defaults to OCI, which is largely compatible with most recent container runtimes, but Docker may be needed for older runtimes without OCI support.
    /// </param>
    /// <param name = "expand">
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> ExportAsync(string path, Container[]? platformVariants = null, ImageLayerCompression? forcedCompression = null, ImageMediaTypes? mediaTypes = ImageMediaTypes.OCIMediaTypes, bool? expand = false, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path)));
        if (platformVariants is Container[] platformVariants_)
        {
            arguments = arguments.Add(new Argument("platformVariants", new ListValue(platformVariants_.Select(v => new IdValue<ContainerId>(v) as Value).ToList())));
        }

        if (forcedCompression is ImageLayerCompression forcedCompression_)
        {
            arguments = arguments.Add(new Argument("forcedCompression", new StringValue(forcedCompression_.ToString())));
        }

        if (mediaTypes is ImageMediaTypes mediaTypes_)
        {
            arguments = arguments.Add(new Argument("mediaTypes", new StringValue(mediaTypes_.ToString())));
        }

        if (expand is bool expand_)
        {
            arguments = arguments.Add(new Argument("expand", new BooleanValue(expand_)));
        }

        var queryBuilder = QueryBuilder.Select("export", arguments);
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Exports the container as an image to the host's container image store.
    /// </summary>
    /// <param name = "name">
    /// Name of image to export to in the host's store
    /// </param>
    /// <param name = "platformVariants">
    /// Identifiers for other platform specific containers.
    /// 
    /// Used for multi-platform image.
    /// </param>
    /// <param name = "forcedCompression">
    /// Force each layer of the exported image to use the specified compression algorithm.
    /// 
    /// If this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.
    /// </param>
    /// <param name = "mediaTypes">
    /// Use the specified media types for the exported image's layers.
    /// 
    /// Defaults to OCI, which is largely compatible with most recent container runtimes, but Docker may be needed for older runtimes without OCI support.
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Void> ExportImageAsync(string name, Container[]? platformVariants = null, ImageLayerCompression? forcedCompression = null, ImageMediaTypes? mediaTypes = ImageMediaTypes.OCIMediaTypes, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name)));
        if (platformVariants is Container[] platformVariants_)
        {
            arguments = arguments.Add(new Argument("platformVariants", new ListValue(platformVariants_.Select(v => new IdValue<ContainerId>(v) as Value).ToList())));
        }

        if (forcedCompression is ImageLayerCompression forcedCompression_)
        {
            arguments = arguments.Add(new Argument("forcedCompression", new StringValue(forcedCompression_.ToString())));
        }

        if (mediaTypes is ImageMediaTypes mediaTypes_)
        {
            arguments = arguments.Add(new Argument("mediaTypes", new StringValue(mediaTypes_.ToString())));
        }

        var queryBuilder = QueryBuilder.Select("exportImage", arguments);
        return await QueryExecutor.ExecuteAsync<Void>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Retrieves the list of exposed ports.
    ///
    /// This includes ports already exposed by the image, even if not explicitly added with dagger.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Port[]> ExposedPortsAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("exposedPorts").Select("id");
        return (await QueryExecutor.ExecuteListAsync<PortId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new Port(QueryBuilder.Builder().Select("loadPortFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// Retrieves a file at the given path.
    ///
    /// Mounts are included.
    /// </summary>
    /// <param name = "path">
    /// The path of the file to retrieve (e.g., "./README.md").
    /// </param>
    /// <param name = "expand">
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
    /// </param>
    public File File(string path, bool? expand = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path)));
        if (expand is bool expand_)
        {
            arguments = arguments.Add(new Argument("expand", new BooleanValue(expand_)));
        }

        var queryBuilder = QueryBuilder.Select("file", arguments);
        return new File(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Download a container image, and apply it to the container state. All previous state will be lost.
    /// </summary>
    /// <param name = "address">
    /// Address of the container image to download, in standard OCI ref format. Example:"registry.dagger.io/engine:latest"
    /// </param>
    public Container From(string address)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("address", new StringValue(address)));
        var queryBuilder = QueryBuilder.Select("from", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// A unique identifier for this Container.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<ContainerId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<ContainerId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The unique image reference which can only be retrieved immediately after the 'Container.From' call.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> ImageRefAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("imageRef");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Reads the container from an OCI tarball.
    /// </summary>
    /// <param name = "source">
    /// File to read the container from.
    /// </param>
    /// <param name = "tag">
    /// Identifies the tag to import from the archive, if the archive bundles multiple tags.
    /// </param>
    public Container Import(File source, string? tag = null)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("source", new IdValue<FileId>(source)));
        if (tag is string tag_)
        {
            arguments = arguments.Add(new Argument("tag", new StringValue(tag_)));
        }

        var queryBuilder = QueryBuilder.Select("import", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves the value of the specified label.
    /// </summary>
    /// <param name = "name">
    /// The name of the label (e.g., "org.opencontainers.artifact.created").
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> LabelAsync(string name, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name)));
        var queryBuilder = QueryBuilder.Select("label", arguments);
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Retrieves the list of labels passed to container.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Label[]> LabelsAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("labels").Select("id");
        return (await QueryExecutor.ExecuteListAsync<LabelId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new Label(QueryBuilder.Builder().Select("loadLabelFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// Retrieves the list of paths where a directory is mounted.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string[]> MountsAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("mounts");
        return await QueryExecutor.ExecuteListAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The platform this container executes and publishes as.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Platform> PlatformAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("platform");
        return await QueryExecutor.ExecuteAsync<Platform>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Package the container state as an OCI image, and publish it to a registry
    ///
    /// Returns the fully qualified address of the published image, with digest
    /// </summary>
    /// <param name = "address">
    /// The OCI address to publish to
    /// 
    /// Same format as "docker push". Example: "registry.example.com/user/repo:tag"
    /// </param>
    /// <param name = "platformVariants">
    /// Identifiers for other platform specific containers.
    /// 
    /// Used for multi-platform image.
    /// </param>
    /// <param name = "forcedCompression">
    /// Force each layer of the published image to use the specified compression algorithm.
    /// 
    /// If this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.
    /// </param>
    /// <param name = "mediaTypes">
    /// Use the specified media types for the published image's layers.
    /// 
    /// Defaults to "OCI", which is compatible with most recent registries, but "Docker" may be needed for older registries without OCI support.
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> PublishAsync(string address, Container[]? platformVariants = null, ImageLayerCompression? forcedCompression = null, ImageMediaTypes? mediaTypes = ImageMediaTypes.OCIMediaTypes, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("address", new StringValue(address)));
        if (platformVariants is Container[] platformVariants_)
        {
            arguments = arguments.Add(new Argument("platformVariants", new ListValue(platformVariants_.Select(v => new IdValue<ContainerId>(v) as Value).ToList())));
        }

        if (forcedCompression is ImageLayerCompression forcedCompression_)
        {
            arguments = arguments.Add(new Argument("forcedCompression", new StringValue(forcedCompression_.ToString())));
        }

        if (mediaTypes is ImageMediaTypes mediaTypes_)
        {
            arguments = arguments.Add(new Argument("mediaTypes", new StringValue(mediaTypes_.ToString())));
        }

        var queryBuilder = QueryBuilder.Select("publish", arguments);
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Return a snapshot of the container's root filesystem. The snapshot can be modified then written back using withRootfs. Use that method for filesystem modifications.
    /// </summary>
    public Directory Rootfs()
    {
        var queryBuilder = QueryBuilder.Select("rootfs");
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// The buffered standard error stream of the last executed command
    ///
    /// Returns an error if no command was executed
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> StderrAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("stderr");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The buffered standard output stream of the last executed command
    ///
    /// Returns an error if no command was executed
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> StdoutAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("stdout");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Forces evaluation of the pipeline in the engine.
    ///
    /// It doesn't run the default command if no exec has been set.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<ContainerId> SyncAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("sync");
        return await QueryExecutor.ExecuteAsync<ContainerId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Opens an interactive terminal for this container using its configured default terminal command if not overridden by args (or sh as a fallback default).
    /// </summary>
    /// <param name = "cmd">
    /// If set, override the container's default terminal command and invoke these command arguments instead.
    /// </param>
    /// <param name = "experimentalPrivilegedNesting">
    /// Provides Dagger access to the executed command.
    /// </param>
    /// <param name = "insecureRootCapabilities">
    /// Execute the command with all root capabilities. This is similar to running a command with "sudo" or executing "docker run" with the "--privileged" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.
    /// </param>
    public Container Terminal(string[]? cmd = null, bool? experimentalPrivilegedNesting = false, bool? insecureRootCapabilities = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (cmd is string[] cmd_)
        {
            arguments = arguments.Add(new Argument("cmd", new ListValue(cmd_.Select(v => new StringValue(v) as Value).ToList())));
        }

        if (experimentalPrivilegedNesting is bool experimentalPrivilegedNesting_)
        {
            arguments = arguments.Add(new Argument("experimentalPrivilegedNesting", new BooleanValue(experimentalPrivilegedNesting_)));
        }

        if (insecureRootCapabilities is bool insecureRootCapabilities_)
        {
            arguments = arguments.Add(new Argument("insecureRootCapabilities", new BooleanValue(insecureRootCapabilities_)));
        }

        var queryBuilder = QueryBuilder.Select("terminal", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Starts a Service and creates a tunnel that forwards traffic from the caller's network to that service.
    ///
    /// Be sure to set any exposed ports before calling this api.
    /// </summary>
    /// <param name = "random">
    /// Bind each tunnel port to a random port on the host.
    /// </param>
    /// <param name = "ports">
    /// List of frontend/backend port mappings to forward.
    /// 
    /// Frontend is the port accepting traffic on the host, backend is the service port.
    /// </param>
    /// <param name = "args">
    /// Command to run instead of the container's default command (e.g., ["go", "run", "main.go"]).
    /// 
    /// If empty, the container's default command is used.
    /// </param>
    /// <param name = "useEntrypoint">
    /// If the container has an entrypoint, prepend it to the args.
    /// </param>
    /// <param name = "experimentalPrivilegedNesting">
    /// Provides Dagger access to the executed command.
    /// </param>
    /// <param name = "insecureRootCapabilities">
    /// Execute the command with all root capabilities. This is similar to running a command with "sudo" or executing "docker run" with the "--privileged" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.
    /// </param>
    /// <param name = "expand">
    /// Replace "${VAR}" or "$VAR" in the args according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    /// </param>
    /// <param name = "noInit">
    /// If set, skip the automatic init process injected into containers by default.
    /// 
    /// This should only be used if the user requires that their exec process be the pid 1 process in the container. Otherwise it may result in unexpected behavior.
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Void> UpAsync(bool? random = false, PortForward[]? ports = null, string[]? args = null, bool? useEntrypoint = false, bool? experimentalPrivilegedNesting = false, bool? insecureRootCapabilities = false, bool? expand = false, bool? noInit = false, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (random is bool random_)
        {
            arguments = arguments.Add(new Argument("random", new BooleanValue(random_)));
        }

        if (ports is PortForward[] ports_)
        {
            arguments = arguments.Add(new Argument("ports", new ListValue(ports_.Select(v => new ObjectValue(v.ToKeyValuePairs()) as Value).ToList())));
        }

        if (args is string[] args_)
        {
            arguments = arguments.Add(new Argument("args", new ListValue(args_.Select(v => new StringValue(v) as Value).ToList())));
        }

        if (useEntrypoint is bool useEntrypoint_)
        {
            arguments = arguments.Add(new Argument("useEntrypoint", new BooleanValue(useEntrypoint_)));
        }

        if (experimentalPrivilegedNesting is bool experimentalPrivilegedNesting_)
        {
            arguments = arguments.Add(new Argument("experimentalPrivilegedNesting", new BooleanValue(experimentalPrivilegedNesting_)));
        }

        if (insecureRootCapabilities is bool insecureRootCapabilities_)
        {
            arguments = arguments.Add(new Argument("insecureRootCapabilities", new BooleanValue(insecureRootCapabilities_)));
        }

        if (expand is bool expand_)
        {
            arguments = arguments.Add(new Argument("expand", new BooleanValue(expand_)));
        }

        if (noInit is bool noInit_)
        {
            arguments = arguments.Add(new Argument("noInit", new BooleanValue(noInit_)));
        }

        var queryBuilder = QueryBuilder.Select("up", arguments);
        return await QueryExecutor.ExecuteAsync<Void>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Retrieves the user to be set for all commands.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> UserAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("user");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Retrieves this container plus the given OCI anotation.
    /// </summary>
    /// <param name = "name">
    /// The name of the annotation.
    /// </param>
    /// <param name = "value">
    /// The value of the annotation.
    /// </param>
    public Container WithAnnotation(string name, string value)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new StringValue(value)));
        var queryBuilder = QueryBuilder.Select("withAnnotation", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Configures default arguments for future commands. Like CMD in Dockerfile.
    /// </summary>
    /// <param name = "args">
    /// Arguments to prepend to future executions (e.g., ["-v", "--no-cache"]).
    /// </param>
    public Container WithDefaultArgs(string[] args)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("args", new ListValue(args.Select(v => new StringValue(v) as Value).ToList())));
        var queryBuilder = QueryBuilder.Select("withDefaultArgs", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Set the default command to invoke for the container's terminal API.
    /// </summary>
    /// <param name = "args">
    /// The args of the command.
    /// </param>
    /// <param name = "experimentalPrivilegedNesting">
    /// Provides Dagger access to the executed command.
    /// </param>
    /// <param name = "insecureRootCapabilities">
    /// Execute the command with all root capabilities. This is similar to running a command with "sudo" or executing "docker run" with the "--privileged" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.
    /// </param>
    public Container WithDefaultTerminalCmd(string[] args, bool? experimentalPrivilegedNesting = false, bool? insecureRootCapabilities = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("args", new ListValue(args.Select(v => new StringValue(v) as Value).ToList())));
        if (experimentalPrivilegedNesting is bool experimentalPrivilegedNesting_)
        {
            arguments = arguments.Add(new Argument("experimentalPrivilegedNesting", new BooleanValue(experimentalPrivilegedNesting_)));
        }

        if (insecureRootCapabilities is bool insecureRootCapabilities_)
        {
            arguments = arguments.Add(new Argument("insecureRootCapabilities", new BooleanValue(insecureRootCapabilities_)));
        }

        var queryBuilder = QueryBuilder.Select("withDefaultTerminalCmd", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Return a new container snapshot, with a directory added to its filesystem
    /// </summary>
    /// <param name = "path">
    /// Location of the written directory (e.g., "/tmp/directory").
    /// </param>
    /// <param name = "source">
    /// Identifier of the directory to write
    /// </param>
    /// <param name = "exclude">
    /// Patterns to exclude in the written directory (e.g. ["node_modules/**", ".gitignore", ".git/"]).
    /// </param>
    /// <param name = "include">
    /// Patterns to include in the written directory (e.g. ["*.go", "go.mod", "go.sum"]).
    /// </param>
    /// <param name = "gitignore">
    /// Apply .gitignore rules when writing the directory.
    /// </param>
    /// <param name = "owner">
    /// A user:group to set for the directory and its contents.
    /// 
    /// The user and group can either be an ID (1000:1000) or a name (foo:bar).
    /// 
    /// If the group is omitted, it defaults to the same as the user.
    /// </param>
    /// <param name = "expand">
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    /// </param>
    public Container WithDirectory(string path, Directory source, string[]? exclude = null, string[]? include = null, bool? gitignore = false, string? owner = null, bool? expand = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path))).Add(new Argument("source", new IdValue<DirectoryId>(source)));
        if (exclude is string[] exclude_)
        {
            arguments = arguments.Add(new Argument("exclude", new ListValue(exclude_.Select(v => new StringValue(v) as Value).ToList())));
        }

        if (include is string[] include_)
        {
            arguments = arguments.Add(new Argument("include", new ListValue(include_.Select(v => new StringValue(v) as Value).ToList())));
        }

        if (gitignore is bool gitignore_)
        {
            arguments = arguments.Add(new Argument("gitignore", new BooleanValue(gitignore_)));
        }

        if (owner is string owner_)
        {
            arguments = arguments.Add(new Argument("owner", new StringValue(owner_)));
        }

        if (expand is bool expand_)
        {
            arguments = arguments.Add(new Argument("expand", new BooleanValue(expand_)));
        }

        var queryBuilder = QueryBuilder.Select("withDirectory", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Set an OCI-style entrypoint. It will be included in the container's OCI configuration. Note, withExec ignores the entrypoint by default.
    /// </summary>
    /// <param name = "args">
    /// Arguments of the entrypoint. Example: ["go", "run"].
    /// </param>
    /// <param name = "keepDefaultArgs">
    /// Don't reset the default arguments when setting the entrypoint. By default it is reset, since entrypoint and default args are often tightly coupled.
    /// </param>
    public Container WithEntrypoint(string[] args, bool? keepDefaultArgs = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("args", new ListValue(args.Select(v => new StringValue(v) as Value).ToList())));
        if (keepDefaultArgs is bool keepDefaultArgs_)
        {
            arguments = arguments.Add(new Argument("keepDefaultArgs", new BooleanValue(keepDefaultArgs_)));
        }

        var queryBuilder = QueryBuilder.Select("withEntrypoint", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Set a new environment variable in the container.
    /// </summary>
    /// <param name = "name">
    /// Name of the environment variable (e.g., "HOST").
    /// </param>
    /// <param name = "value">
    /// Value of the environment variable. (e.g., "localhost").
    /// </param>
    /// <param name = "expand">
    /// Replace "${VAR}" or "$VAR" in the value according to the current environment variables defined in the container (e.g. "/opt/bin:$PATH").
    /// </param>
    public Container WithEnvVariable(string name, string value, bool? expand = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new StringValue(value)));
        if (expand is bool expand_)
        {
            arguments = arguments.Add(new Argument("expand", new BooleanValue(expand_)));
        }

        var queryBuilder = QueryBuilder.Select("withEnvVariable", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Raise an error.
    /// </summary>
    /// <param name = "err">
    /// Message of the error to raise. If empty, the error will be ignored.
    /// </param>
    public Container WithError(string err)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("err", new StringValue(err)));
        var queryBuilder = QueryBuilder.Select("withError", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Execute a command in the container, and return a new snapshot of the container state after execution.
    /// </summary>
    /// <param name = "args">
    /// Command to execute. Must be valid exec() arguments, not a shell command. Example: ["go", "run", "main.go"].
    /// 
    /// To run a shell command, execute the shell and pass the shell command as argument. Example: ["sh", "-c", "ls -l | grep foo"]
    /// 
    /// Defaults to the container's default arguments (see "defaultArgs" and "withDefaultArgs").
    /// </param>
    /// <param name = "useEntrypoint">
    /// Apply the OCI entrypoint, if present, by prepending it to the args. Ignored by default.
    /// </param>
    /// <param name = "stdin">
    /// Content to write to the command's standard input. Example: "Hello world")
    /// </param>
    /// <param name = "redirectStdin">
    /// Redirect the command's standard input from a file in the container. Example: "./stdin.txt"
    /// </param>
    /// <param name = "redirectStdout">
    /// Redirect the command's standard output to a file in the container. Example: "./stdout.txt"
    /// </param>
    /// <param name = "redirectStderr">
    /// Redirect the command's standard error to a file in the container. Example: "./stderr.txt"
    /// </param>
    /// <param name = "expect">
    /// Exit codes this command is allowed to exit with without error
    /// </param>
    /// <param name = "experimentalPrivilegedNesting">
    /// Provides Dagger access to the executed command.
    /// </param>
    /// <param name = "insecureRootCapabilities">
    /// Execute the command with all root capabilities. Like --privileged in Docker
    /// 
    /// DANGER: this grants the command full access to the host system. Only use when 1) you trust the command being executed and 2) you specifically need this level of access.
    /// </param>
    /// <param name = "expand">
    /// Replace "${VAR}" or "$VAR" in the args according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    /// </param>
    /// <param name = "noInit">
    /// Skip the automatic init process injected into containers by default.
    /// 
    /// Only use this if you specifically need the command to be pid 1 in the container. Otherwise it may result in unexpected behavior. If you're not sure, you don't need this.
    /// </param>
    public Container WithExec(string[] args, bool? useEntrypoint = false, string? stdin = null, string? redirectStdin = null, string? redirectStdout = null, string? redirectStderr = null, ReturnType? expect = ReturnType.SUCCESS, bool? experimentalPrivilegedNesting = false, bool? insecureRootCapabilities = false, bool? expand = false, bool? noInit = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("args", new ListValue(args.Select(v => new StringValue(v) as Value).ToList())));
        if (useEntrypoint is bool useEntrypoint_)
        {
            arguments = arguments.Add(new Argument("useEntrypoint", new BooleanValue(useEntrypoint_)));
        }

        if (stdin is string stdin_)
        {
            arguments = arguments.Add(new Argument("stdin", new StringValue(stdin_)));
        }

        if (redirectStdin is string redirectStdin_)
        {
            arguments = arguments.Add(new Argument("redirectStdin", new StringValue(redirectStdin_)));
        }

        if (redirectStdout is string redirectStdout_)
        {
            arguments = arguments.Add(new Argument("redirectStdout", new StringValue(redirectStdout_)));
        }

        if (redirectStderr is string redirectStderr_)
        {
            arguments = arguments.Add(new Argument("redirectStderr", new StringValue(redirectStderr_)));
        }

        if (expect is ReturnType expect_)
        {
            arguments = arguments.Add(new Argument("expect", new StringValue(expect_.ToString())));
        }

        if (experimentalPrivilegedNesting is bool experimentalPrivilegedNesting_)
        {
            arguments = arguments.Add(new Argument("experimentalPrivilegedNesting", new BooleanValue(experimentalPrivilegedNesting_)));
        }

        if (insecureRootCapabilities is bool insecureRootCapabilities_)
        {
            arguments = arguments.Add(new Argument("insecureRootCapabilities", new BooleanValue(insecureRootCapabilities_)));
        }

        if (expand is bool expand_)
        {
            arguments = arguments.Add(new Argument("expand", new BooleanValue(expand_)));
        }

        if (noInit is bool noInit_)
        {
            arguments = arguments.Add(new Argument("noInit", new BooleanValue(noInit_)));
        }

        var queryBuilder = QueryBuilder.Select("withExec", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Expose a network port. Like EXPOSE in Dockerfile (but with healthcheck support)
    ///
    /// Exposed ports serve two purposes:
    ///
    /// - For health checks and introspection, when running services
    ///
    /// - For setting the EXPOSE OCI field when publishing the container
    /// </summary>
    /// <param name = "port">
    /// Port number to expose. Example: 8080
    /// </param>
    /// <param name = "protocol">
    /// Network protocol. Example: "tcp"
    /// </param>
    /// <param name = "description">
    /// Port description. Example: "payment API endpoint"
    /// </param>
    /// <param name = "experimentalSkipHealthcheck">
    /// Skip the health check when run as a service.
    /// </param>
    public Container WithExposedPort(int port, NetworkProtocol? protocol = NetworkProtocol.TCP, string? description = null, bool? experimentalSkipHealthcheck = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("port", new IntValue(port)));
        if (protocol is NetworkProtocol protocol_)
        {
            arguments = arguments.Add(new Argument("protocol", new StringValue(protocol_.ToString())));
        }

        if (description is string description_)
        {
            arguments = arguments.Add(new Argument("description", new StringValue(description_)));
        }

        if (experimentalSkipHealthcheck is bool experimentalSkipHealthcheck_)
        {
            arguments = arguments.Add(new Argument("experimentalSkipHealthcheck", new BooleanValue(experimentalSkipHealthcheck_)));
        }

        var queryBuilder = QueryBuilder.Select("withExposedPort", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Return a container snapshot with a file added
    /// </summary>
    /// <param name = "path">
    /// Path of the new file. Example: "/path/to/new-file.txt"
    /// </param>
    /// <param name = "source">
    /// File to add
    /// </param>
    /// <param name = "permissions">
    /// Permissions of the new file. Example: 0600
    /// </param>
    /// <param name = "owner">
    /// A user:group to set for the file.
    /// 
    /// The user and group can either be an ID (1000:1000) or a name (foo:bar).
    /// 
    /// If the group is omitted, it defaults to the same as the user.
    /// </param>
    /// <param name = "expand">
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
    /// </param>
    public Container WithFile(string path, File source, int? permissions = null, string? owner = null, bool? expand = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path))).Add(new Argument("source", new IdValue<FileId>(source)));
        if (permissions is int permissions_)
        {
            arguments = arguments.Add(new Argument("permissions", new IntValue(permissions_)));
        }

        if (owner is string owner_)
        {
            arguments = arguments.Add(new Argument("owner", new StringValue(owner_)));
        }

        if (expand is bool expand_)
        {
            arguments = arguments.Add(new Argument("expand", new BooleanValue(expand_)));
        }

        var queryBuilder = QueryBuilder.Select("withFile", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves this container plus the contents of the given files copied to the given path.
    /// </summary>
    /// <param name = "path">
    /// Location where copied files should be placed (e.g., "/src").
    /// </param>
    /// <param name = "sources">
    /// Identifiers of the files to copy.
    /// </param>
    /// <param name = "permissions">
    /// Permission given to the copied files (e.g., 0600).
    /// </param>
    /// <param name = "owner">
    /// A user:group to set for the files.
    /// 
    /// The user and group can either be an ID (1000:1000) or a name (foo:bar).
    /// 
    /// If the group is omitted, it defaults to the same as the user.
    /// </param>
    /// <param name = "expand">
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
    /// </param>
    public Container WithFiles(string path, File[] sources, int? permissions = null, string? owner = null, bool? expand = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path))).Add(new Argument("sources", new ListValue(sources.Select(v => new IdValue<FileId>(v) as Value).ToList())));
        if (permissions is int permissions_)
        {
            arguments = arguments.Add(new Argument("permissions", new IntValue(permissions_)));
        }

        if (owner is string owner_)
        {
            arguments = arguments.Add(new Argument("owner", new StringValue(owner_)));
        }

        if (expand is bool expand_)
        {
            arguments = arguments.Add(new Argument("expand", new BooleanValue(expand_)));
        }

        var queryBuilder = QueryBuilder.Select("withFiles", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves this container plus the given label.
    /// </summary>
    /// <param name = "name">
    /// The name of the label (e.g., "org.opencontainers.artifact.created").
    /// </param>
    /// <param name = "value">
    /// The value of the label (e.g., "2023-01-01T00:00:00Z").
    /// </param>
    public Container WithLabel(string name, string value)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new StringValue(value)));
        var queryBuilder = QueryBuilder.Select("withLabel", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves this container plus a cache volume mounted at the given path.
    /// </summary>
    /// <param name = "path">
    /// Location of the cache directory (e.g., "/root/.npm").
    /// </param>
    /// <param name = "cache">
    /// Identifier of the cache volume to mount.
    /// </param>
    /// <param name = "source">
    /// Identifier of the directory to use as the cache volume's root.
    /// </param>
    /// <param name = "sharing">
    /// Sharing mode of the cache volume.
    /// </param>
    /// <param name = "owner">
    /// A user:group to set for the mounted cache directory.
    /// 
    /// Note that this changes the ownership of the specified mount along with the initial filesystem provided by source (if any). It does not have any effect if/when the cache has already been created.
    /// 
    /// The user and group can either be an ID (1000:1000) or a name (foo:bar).
    /// 
    /// If the group is omitted, it defaults to the same as the user.
    /// </param>
    /// <param name = "expand">
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    /// </param>
    public Container WithMountedCache(string path, CacheVolume cache, Directory? source = null, CacheSharingMode? sharing = CacheSharingMode.SHARED, string? owner = null, bool? expand = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path))).Add(new Argument("cache", new IdValue<CacheVolumeId>(cache)));
        if (source is Directory source_)
        {
            arguments = arguments.Add(new Argument("source", new IdValue<DirectoryId>(source_)));
        }

        if (sharing is CacheSharingMode sharing_)
        {
            arguments = arguments.Add(new Argument("sharing", new StringValue(sharing_.ToString())));
        }

        if (owner is string owner_)
        {
            arguments = arguments.Add(new Argument("owner", new StringValue(owner_)));
        }

        if (expand is bool expand_)
        {
            arguments = arguments.Add(new Argument("expand", new BooleanValue(expand_)));
        }

        var queryBuilder = QueryBuilder.Select("withMountedCache", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves this container plus a directory mounted at the given path.
    /// </summary>
    /// <param name = "path">
    /// Location of the mounted directory (e.g., "/mnt/directory").
    /// </param>
    /// <param name = "source">
    /// Identifier of the mounted directory.
    /// </param>
    /// <param name = "owner">
    /// A user:group to set for the mounted directory and its contents.
    /// 
    /// The user and group can either be an ID (1000:1000) or a name (foo:bar).
    /// 
    /// If the group is omitted, it defaults to the same as the user.
    /// </param>
    /// <param name = "expand">
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    /// </param>
    public Container WithMountedDirectory(string path, Directory source, string? owner = null, bool? expand = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path))).Add(new Argument("source", new IdValue<DirectoryId>(source)));
        if (owner is string owner_)
        {
            arguments = arguments.Add(new Argument("owner", new StringValue(owner_)));
        }

        if (expand is bool expand_)
        {
            arguments = arguments.Add(new Argument("expand", new BooleanValue(expand_)));
        }

        var queryBuilder = QueryBuilder.Select("withMountedDirectory", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves this container plus a file mounted at the given path.
    /// </summary>
    /// <param name = "path">
    /// Location of the mounted file (e.g., "/tmp/file.txt").
    /// </param>
    /// <param name = "source">
    /// Identifier of the mounted file.
    /// </param>
    /// <param name = "owner">
    /// A user or user:group to set for the mounted file.
    /// 
    /// The user and group can either be an ID (1000:1000) or a name (foo:bar).
    /// 
    /// If the group is omitted, it defaults to the same as the user.
    /// </param>
    /// <param name = "expand">
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
    /// </param>
    public Container WithMountedFile(string path, File source, string? owner = null, bool? expand = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path))).Add(new Argument("source", new IdValue<FileId>(source)));
        if (owner is string owner_)
        {
            arguments = arguments.Add(new Argument("owner", new StringValue(owner_)));
        }

        if (expand is bool expand_)
        {
            arguments = arguments.Add(new Argument("expand", new BooleanValue(expand_)));
        }

        var queryBuilder = QueryBuilder.Select("withMountedFile", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves this container plus a secret mounted into a file at the given path.
    /// </summary>
    /// <param name = "path">
    /// Location of the secret file (e.g., "/tmp/secret.txt").
    /// </param>
    /// <param name = "source">
    /// Identifier of the secret to mount.
    /// </param>
    /// <param name = "owner">
    /// A user:group to set for the mounted secret.
    /// 
    /// The user and group can either be an ID (1000:1000) or a name (foo:bar).
    /// 
    /// If the group is omitted, it defaults to the same as the user.
    /// </param>
    /// <param name = "mode">
    /// Permission given to the mounted secret (e.g., 0600).
    /// 
    /// This option requires an owner to be set to be active.
    /// </param>
    /// <param name = "expand">
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    /// </param>
    public Container WithMountedSecret(string path, Secret source, string? owner = null, int? mode = 256, bool? expand = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path))).Add(new Argument("source", new IdValue<SecretId>(source)));
        if (owner is string owner_)
        {
            arguments = arguments.Add(new Argument("owner", new StringValue(owner_)));
        }

        if (mode is int mode_)
        {
            arguments = arguments.Add(new Argument("mode", new IntValue(mode_)));
        }

        if (expand is bool expand_)
        {
            arguments = arguments.Add(new Argument("expand", new BooleanValue(expand_)));
        }

        var queryBuilder = QueryBuilder.Select("withMountedSecret", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves this container plus a temporary directory mounted at the given path. Any writes will be ephemeral to a single withExec call; they will not be persisted to subsequent withExecs.
    /// </summary>
    /// <param name = "path">
    /// Location of the temporary directory (e.g., "/tmp/temp_dir").
    /// </param>
    /// <param name = "size">
    /// Size of the temporary directory in bytes.
    /// </param>
    /// <param name = "expand">
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    /// </param>
    public Container WithMountedTemp(string path, int? size = null, bool? expand = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path)));
        if (size is int size_)
        {
            arguments = arguments.Add(new Argument("size", new IntValue(size_)));
        }

        if (expand is bool expand_)
        {
            arguments = arguments.Add(new Argument("expand", new BooleanValue(expand_)));
        }

        var queryBuilder = QueryBuilder.Select("withMountedTemp", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Return a new container snapshot, with a file added to its filesystem with text content
    /// </summary>
    /// <param name = "path">
    /// Path of the new file. May be relative or absolute. Example: "README.md" or "/etc/profile"
    /// </param>
    /// <param name = "contents">
    /// Contents of the new file. Example: "Hello world!"
    /// </param>
    /// <param name = "permissions">
    /// Permissions of the new file. Example: 0600
    /// </param>
    /// <param name = "owner">
    /// A user:group to set for the file.
    /// 
    /// The user and group can either be an ID (1000:1000) or a name (foo:bar).
    /// 
    /// If the group is omitted, it defaults to the same as the user.
    /// </param>
    /// <param name = "expand">
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
    /// </param>
    public Container WithNewFile(string path, string contents, int? permissions = 420, string? owner = null, bool? expand = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path))).Add(new Argument("contents", new StringValue(contents)));
        if (permissions is int permissions_)
        {
            arguments = arguments.Add(new Argument("permissions", new IntValue(permissions_)));
        }

        if (owner is string owner_)
        {
            arguments = arguments.Add(new Argument("owner", new StringValue(owner_)));
        }

        if (expand is bool expand_)
        {
            arguments = arguments.Add(new Argument("expand", new BooleanValue(expand_)));
        }

        var queryBuilder = QueryBuilder.Select("withNewFile", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Attach credentials for future publishing to a registry. Use in combination with publish
    /// </summary>
    /// <param name = "address">
    /// The image address that needs authentication. Same format as "docker push". Example: "registry.dagger.io/dagger:latest"
    /// </param>
    /// <param name = "username">
    /// The username to authenticate with. Example: "alice"
    /// </param>
    /// <param name = "secret">
    /// The API key, password or token to authenticate to this registry
    /// </param>
    public Container WithRegistryAuth(string address, string username, Secret secret)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("address", new StringValue(address))).Add(new Argument("username", new StringValue(username))).Add(new Argument("secret", new IdValue<SecretId>(secret)));
        var queryBuilder = QueryBuilder.Select("withRegistryAuth", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Change the container's root filesystem. The previous root filesystem will be lost.
    /// </summary>
    /// <param name = "directory">
    /// The new root filesystem.
    /// </param>
    public Container WithRootfs(Directory directory)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("directory", new IdValue<DirectoryId>(directory)));
        var queryBuilder = QueryBuilder.Select("withRootfs", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Set a new environment variable, using a secret value
    /// </summary>
    /// <param name = "name">
    /// Name of the secret variable (e.g., "API_SECRET").
    /// </param>
    /// <param name = "secret">
    /// Identifier of the secret value.
    /// </param>
    public Container WithSecretVariable(string name, Secret secret)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("secret", new IdValue<SecretId>(secret)));
        var queryBuilder = QueryBuilder.Select("withSecretVariable", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Establish a runtime dependency from a container to a network service.
    ///
    /// The service will be started automatically when needed and detached when it is no longer needed, executing the default command if none is set.
    ///
    /// The service will be reachable from the container via the provided hostname alias.
    ///
    /// The service dependency will also convey to any files or directories produced by the container.
    /// </summary>
    /// <param name = "alias">
    /// Hostname that will resolve to the target service (only accessible from within this container)
    /// </param>
    /// <param name = "service">
    /// The target service
    /// </param>
    public Container WithServiceBinding(string alias, Service service)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("alias", new StringValue(alias))).Add(new Argument("service", new IdValue<ServiceId>(service)));
        var queryBuilder = QueryBuilder.Select("withServiceBinding", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Return a snapshot with a symlink
    /// </summary>
    /// <param name = "target">
    /// Location of the file or directory to link to (e.g., "/existing/file").
    /// </param>
    /// <param name = "linkName">
    /// Location where the symbolic link will be created (e.g., "/new-file-link").
    /// </param>
    /// <param name = "expand">
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
    /// </param>
    public Container WithSymlink(string target, string linkName, bool? expand = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("target", new StringValue(target))).Add(new Argument("linkName", new StringValue(linkName)));
        if (expand is bool expand_)
        {
            arguments = arguments.Add(new Argument("expand", new BooleanValue(expand_)));
        }

        var queryBuilder = QueryBuilder.Select("withSymlink", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves this container plus a socket forwarded to the given Unix socket path.
    /// </summary>
    /// <param name = "path">
    /// Location of the forwarded Unix socket (e.g., "/tmp/socket").
    /// </param>
    /// <param name = "source">
    /// Identifier of the socket to forward.
    /// </param>
    /// <param name = "owner">
    /// A user:group to set for the mounted socket.
    /// 
    /// The user and group can either be an ID (1000:1000) or a name (foo:bar).
    /// 
    /// If the group is omitted, it defaults to the same as the user.
    /// </param>
    /// <param name = "expand">
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    /// </param>
    public Container WithUnixSocket(string path, Socket source, string? owner = null, bool? expand = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path))).Add(new Argument("source", new IdValue<SocketId>(source)));
        if (owner is string owner_)
        {
            arguments = arguments.Add(new Argument("owner", new StringValue(owner_)));
        }

        if (expand is bool expand_)
        {
            arguments = arguments.Add(new Argument("expand", new BooleanValue(expand_)));
        }

        var queryBuilder = QueryBuilder.Select("withUnixSocket", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves this container with a different command user.
    /// </summary>
    /// <param name = "name">
    /// The user to set (e.g., "root").
    /// </param>
    public Container WithUser(string name)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name)));
        var queryBuilder = QueryBuilder.Select("withUser", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Change the container's working directory. Like WORKDIR in Dockerfile.
    /// </summary>
    /// <param name = "path">
    /// The path to set as the working directory (e.g., "/app").
    /// </param>
    /// <param name = "expand">
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    /// </param>
    public Container WithWorkdir(string path, bool? expand = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path)));
        if (expand is bool expand_)
        {
            arguments = arguments.Add(new Argument("expand", new BooleanValue(expand_)));
        }

        var queryBuilder = QueryBuilder.Select("withWorkdir", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves this container minus the given OCI annotation.
    /// </summary>
    /// <param name = "name">
    /// The name of the annotation.
    /// </param>
    public Container WithoutAnnotation(string name)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name)));
        var queryBuilder = QueryBuilder.Select("withoutAnnotation", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Remove the container's default arguments.
    /// </summary>
    public Container WithoutDefaultArgs()
    {
        var queryBuilder = QueryBuilder.Select("withoutDefaultArgs");
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Return a new container snapshot, with a directory removed from its filesystem
    /// </summary>
    /// <param name = "path">
    /// Location of the directory to remove (e.g., ".github/").
    /// </param>
    /// <param name = "expand">
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    /// </param>
    public Container WithoutDirectory(string path, bool? expand = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path)));
        if (expand is bool expand_)
        {
            arguments = arguments.Add(new Argument("expand", new BooleanValue(expand_)));
        }

        var queryBuilder = QueryBuilder.Select("withoutDirectory", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Reset the container's OCI entrypoint.
    /// </summary>
    /// <param name = "keepDefaultArgs">
    /// Don't remove the default arguments when unsetting the entrypoint.
    /// </param>
    public Container WithoutEntrypoint(bool? keepDefaultArgs = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (keepDefaultArgs is bool keepDefaultArgs_)
        {
            arguments = arguments.Add(new Argument("keepDefaultArgs", new BooleanValue(keepDefaultArgs_)));
        }

        var queryBuilder = QueryBuilder.Select("withoutEntrypoint", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves this container minus the given environment variable.
    /// </summary>
    /// <param name = "name">
    /// The name of the environment variable (e.g., "HOST").
    /// </param>
    public Container WithoutEnvVariable(string name)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name)));
        var queryBuilder = QueryBuilder.Select("withoutEnvVariable", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Unexpose a previously exposed port.
    /// </summary>
    /// <param name = "port">
    /// Port number to unexpose
    /// </param>
    /// <param name = "protocol">
    /// Port protocol to unexpose
    /// </param>
    public Container WithoutExposedPort(int port, NetworkProtocol? protocol = NetworkProtocol.TCP)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("port", new IntValue(port)));
        if (protocol is NetworkProtocol protocol_)
        {
            arguments = arguments.Add(new Argument("protocol", new StringValue(protocol_.ToString())));
        }

        var queryBuilder = QueryBuilder.Select("withoutExposedPort", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves this container with the file at the given path removed.
    /// </summary>
    /// <param name = "path">
    /// Location of the file to remove (e.g., "/file.txt").
    /// </param>
    /// <param name = "expand">
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
    /// </param>
    public Container WithoutFile(string path, bool? expand = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path)));
        if (expand is bool expand_)
        {
            arguments = arguments.Add(new Argument("expand", new BooleanValue(expand_)));
        }

        var queryBuilder = QueryBuilder.Select("withoutFile", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Return a new container spanshot with specified files removed
    /// </summary>
    /// <param name = "paths">
    /// Paths of the files to remove. Example: ["foo.txt, "/root/.ssh/config"
    /// </param>
    /// <param name = "expand">
    /// Replace "${VAR}" or "$VAR" in the value of paths according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
    /// </param>
    public Container WithoutFiles(string[] paths, bool? expand = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("paths", new ListValue(paths.Select(v => new StringValue(v) as Value).ToList())));
        if (expand is bool expand_)
        {
            arguments = arguments.Add(new Argument("expand", new BooleanValue(expand_)));
        }

        var queryBuilder = QueryBuilder.Select("withoutFiles", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves this container minus the given environment label.
    /// </summary>
    /// <param name = "name">
    /// The name of the label to remove (e.g., "org.opencontainers.artifact.created").
    /// </param>
    public Container WithoutLabel(string name)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name)));
        var queryBuilder = QueryBuilder.Select("withoutLabel", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves this container after unmounting everything at the given path.
    /// </summary>
    /// <param name = "path">
    /// Location of the cache directory (e.g., "/root/.npm").
    /// </param>
    /// <param name = "expand">
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    /// </param>
    public Container WithoutMount(string path, bool? expand = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path)));
        if (expand is bool expand_)
        {
            arguments = arguments.Add(new Argument("expand", new BooleanValue(expand_)));
        }

        var queryBuilder = QueryBuilder.Select("withoutMount", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves this container without the registry authentication of a given address.
    /// </summary>
    /// <param name = "address">
    /// Registry's address to remove the authentication from.
    /// 
    /// Formatted as [host]/[user]/[repo]:[tag] (e.g. docker.io/dagger/dagger:main).
    /// </param>
    public Container WithoutRegistryAuth(string address)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("address", new StringValue(address)));
        var queryBuilder = QueryBuilder.Select("withoutRegistryAuth", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves this container minus the given environment variable containing the secret.
    /// </summary>
    /// <param name = "name">
    /// The name of the environment variable (e.g., "HOST").
    /// </param>
    public Container WithoutSecretVariable(string name)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name)));
        var queryBuilder = QueryBuilder.Select("withoutSecretVariable", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves this container with a previously added Unix socket removed.
    /// </summary>
    /// <param name = "path">
    /// Location of the socket to remove (e.g., "/tmp/socket").
    /// </param>
    /// <param name = "expand">
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    /// </param>
    public Container WithoutUnixSocket(string path, bool? expand = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path)));
        if (expand is bool expand_)
        {
            arguments = arguments.Add(new Argument("expand", new BooleanValue(expand_)));
        }

        var queryBuilder = QueryBuilder.Select("withoutUnixSocket", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves this container with an unset command user.
    ///
    /// Should default to root.
    /// </summary>
    public Container WithoutUser()
    {
        var queryBuilder = QueryBuilder.Select("withoutUser");
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Unset the container's working directory.
    ///
    /// Should default to "/".
    /// </summary>
    public Container WithoutWorkdir()
    {
        var queryBuilder = QueryBuilder.Select("withoutWorkdir");
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves the working directory for all commands.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> WorkdirAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("workdir");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// The `ContainerID` scalar type represents an identifier for an object of type Container.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<ContainerId>))]
public class ContainerId : Scalar
{
}

/// <summary>
/// Reflective module API provided to functions at runtime.
/// </summary>
public class CurrentModule(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<CurrentModuleId>
{
    /// <summary>
    /// The dependencies of the module.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Module[]> DependenciesAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("dependencies").Select("id");
        return (await QueryExecutor.ExecuteListAsync<ModuleId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new Module(QueryBuilder.Builder().Select("loadModuleFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// The generated files and directories made on top of the module source's context directory.
    /// </summary>
    public Directory GeneratedContextDirectory()
    {
        var queryBuilder = QueryBuilder.Select("generatedContextDirectory");
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// A unique identifier for this CurrentModule.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<CurrentModuleId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<CurrentModuleId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The name of the module being executed in
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> NameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("name");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The directory containing the module's source code loaded into the engine (plus any generated code that may have been created).
    /// </summary>
    public Directory Source()
    {
        var queryBuilder = QueryBuilder.Select("source");
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a directory from the module's scratch working directory, including any changes that may have been made to it during module function execution.
    /// </summary>
    /// <param name = "path">
    /// Location of the directory to access (e.g., ".").
    /// </param>
    /// <param name = "exclude">
    /// Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).
    /// </param>
    /// <param name = "include">
    /// Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).
    /// </param>
    /// <param name = "gitignore">
    /// Apply .gitignore filter rules inside the directory
    /// </param>
    public Directory Workdir(string path, string[]? exclude = null, string[]? include = null, bool? gitignore = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path)));
        if (exclude is string[] exclude_)
        {
            arguments = arguments.Add(new Argument("exclude", new ListValue(exclude_.Select(v => new StringValue(v) as Value).ToList())));
        }

        if (include is string[] include_)
        {
            arguments = arguments.Add(new Argument("include", new ListValue(include_.Select(v => new StringValue(v) as Value).ToList())));
        }

        if (gitignore is bool gitignore_)
        {
            arguments = arguments.Add(new Argument("gitignore", new BooleanValue(gitignore_)));
        }

        var queryBuilder = QueryBuilder.Select("workdir", arguments);
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a file from the module's scratch working directory, including any changes that may have been made to it during module function execution.Load a file from the module's scratch working directory, including any changes that may have been made to it during module function execution.
    /// </summary>
    /// <param name = "path">
    /// Location of the file to retrieve (e.g., "README.md").
    /// </param>
    public File WorkdirFile(string path)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path)));
        var queryBuilder = QueryBuilder.Select("workdirFile", arguments);
        return new File(queryBuilder, GraphQLClient);
    }
}

/// <summary>
/// The `CurrentModuleID` scalar type represents an identifier for an object of type CurrentModule.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<CurrentModuleId>))]
public class CurrentModuleId : Scalar
{
}

/// <summary>
/// A directory.
/// </summary>
public class Directory(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<DirectoryId>
{
    /// <summary>
    /// Converts this directory to a local git repository
    /// </summary>
    public GitRepository AsGit()
    {
        var queryBuilder = QueryBuilder.Select("asGit");
        return new GitRepository(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load the directory as a Dagger module source
    /// </summary>
    /// <param name = "sourceRootPath">
    /// An optional subpath of the directory which contains the module's configuration file.
    /// 
    /// If not set, the module source code is loaded from the root of the directory.
    /// </param>
    public Module AsModule(string? sourceRootPath = ".")
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (sourceRootPath is string sourceRootPath_)
        {
            arguments = arguments.Add(new Argument("sourceRootPath", new StringValue(sourceRootPath_)));
        }

        var queryBuilder = QueryBuilder.Select("asModule", arguments);
        return new Module(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load the directory as a Dagger module source
    /// </summary>
    /// <param name = "sourceRootPath">
    /// An optional subpath of the directory which contains the module's configuration file.
    /// 
    /// If not set, the module source code is loaded from the root of the directory.
    /// </param>
    public ModuleSource AsModuleSource(string? sourceRootPath = ".")
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (sourceRootPath is string sourceRootPath_)
        {
            arguments = arguments.Add(new Argument("sourceRootPath", new StringValue(sourceRootPath_)));
        }

        var queryBuilder = QueryBuilder.Select("asModuleSource", arguments);
        return new ModuleSource(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Return the difference between this directory and another directory, typically an older snapshot.
    ///
    /// The difference is encoded as a changeset, which also tracks removed files, and can be applied to other directories.
    /// </summary>
    /// <param name = "from">
    /// The base directory snapshot to compare against
    /// </param>
    public Changeset Changes(Directory from)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("from", new IdValue<DirectoryId>(from)));
        var queryBuilder = QueryBuilder.Select("changes", arguments);
        return new Changeset(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Change the owner of the directory contents recursively.
    /// </summary>
    /// <param name = "path">
    /// Path of the directory to change ownership of (e.g., "/").
    /// </param>
    /// <param name = "owner">
    /// A user:group to set for the mounted directory and its contents.
    /// 
    /// The user and group must be an ID (1000:1000), not a name (foo:bar).
    /// 
    /// If the group is omitted, it defaults to the same as the user.
    /// </param>
    public Directory Chown(string path, string owner)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path))).Add(new Argument("owner", new StringValue(owner)));
        var queryBuilder = QueryBuilder.Select("chown", arguments);
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Return the difference between this directory and an another directory. The difference is encoded as a directory.
    /// </summary>
    /// <param name = "other">
    /// The directory to compare against
    /// </param>
    public Directory Diff(Directory other)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("other", new IdValue<DirectoryId>(other)));
        var queryBuilder = QueryBuilder.Select("diff", arguments);
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Return the directory's digest. The format of the digest is not guaranteed to be stable between releases of Dagger. It is guaranteed to be stable between invocations of the same Dagger engine.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> DigestAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("digest");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Retrieves a directory at the given path.
    /// </summary>
    /// <param name = "path">
    /// Location of the directory to retrieve. Example: "/src"
    /// </param>
    public Directory Directory_(string path)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path)));
        var queryBuilder = QueryBuilder.Select("directory", arguments);
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Use Dockerfile compatibility to build a container from this directory. Only use this function for Dockerfile compatibility. Otherwise use the native Container type directly, it is feature-complete and supports all Dockerfile features.
    /// </summary>
    /// <param name = "dockerfile">
    /// Path to the Dockerfile to use (e.g., "frontend.Dockerfile").
    /// </param>
    /// <param name = "platform">
    /// The platform to build.
    /// </param>
    /// <param name = "buildArgs">
    /// Build arguments to use in the build.
    /// </param>
    /// <param name = "target">
    /// Target build stage to build.
    /// </param>
    /// <param name = "secrets">
    /// Secrets to pass to the build.
    /// 
    /// They will be mounted at /run/secrets/[secret-name].
    /// </param>
    /// <param name = "noInit">
    /// If set, skip the automatic init process injected into containers created by RUN statements.
    /// 
    /// This should only be used if the user requires that their exec processes be the pid 1 process in the container. Otherwise it may result in unexpected behavior.
    /// </param>
    public Container DockerBuild(string? dockerfile = "Dockerfile", Platform? platform = null, BuildArg[]? buildArgs = null, string? target = null, Secret[]? secrets = null, bool? noInit = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (dockerfile is string dockerfile_)
        {
            arguments = arguments.Add(new Argument("dockerfile", new StringValue(dockerfile_)));
        }

        if (platform is Platform platform_)
        {
            arguments = arguments.Add(new Argument("platform", new StringValue(platform_.Value)));
        }

        if (buildArgs is BuildArg[] buildArgs_)
        {
            arguments = arguments.Add(new Argument("buildArgs", new ListValue(buildArgs_.Select(v => new ObjectValue(v.ToKeyValuePairs()) as Value).ToList())));
        }

        if (target is string target_)
        {
            arguments = arguments.Add(new Argument("target", new StringValue(target_)));
        }

        if (secrets is Secret[] secrets_)
        {
            arguments = arguments.Add(new Argument("secrets", new ListValue(secrets_.Select(v => new IdValue<SecretId>(v) as Value).ToList())));
        }

        if (noInit is bool noInit_)
        {
            arguments = arguments.Add(new Argument("noInit", new BooleanValue(noInit_)));
        }

        var queryBuilder = QueryBuilder.Select("dockerBuild", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Returns a list of files and directories at the given path.
    /// </summary>
    /// <param name = "path">
    /// Location of the directory to look at (e.g., "/src").
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string[]> EntriesAsync(string? path = null, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (path is string path_)
        {
            arguments = arguments.Add(new Argument("path", new StringValue(path_)));
        }

        var queryBuilder = QueryBuilder.Select("entries", arguments);
        return await QueryExecutor.ExecuteListAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// check if a file or directory exists
    /// </summary>
    /// <param name = "path">
    /// Path to check (e.g., "/file.txt").
    /// </param>
    /// <param name = "expectedType">
    /// If specified, also validate the type of file (e.g. "REGULAR_TYPE", "DIRECTORY_TYPE", or "SYMLINK_TYPE").
    /// </param>
    /// <param name = "doNotFollowSymlinks">
    /// If specified, do not follow symlinks.
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<bool> ExistsAsync(string path, ExistsType? expectedType = null, bool? doNotFollowSymlinks = false, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path)));
        if (expectedType is ExistsType expectedType_)
        {
            arguments = arguments.Add(new Argument("expectedType", new StringValue(expectedType_.ToString())));
        }

        if (doNotFollowSymlinks is bool doNotFollowSymlinks_)
        {
            arguments = arguments.Add(new Argument("doNotFollowSymlinks", new BooleanValue(doNotFollowSymlinks_)));
        }

        var queryBuilder = QueryBuilder.Select("exists", arguments);
        return await QueryExecutor.ExecuteAsync<bool>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Writes the contents of the directory to a path on the host.
    /// </summary>
    /// <param name = "path">
    /// Location of the copied directory (e.g., "logs/").
    /// </param>
    /// <param name = "wipe">
    /// If true, then the host directory will be wiped clean before exporting so that it exactly matches the directory being exported; this means it will delete any files on the host that aren't in the exported dir. If false (the default), the contents of the directory will be merged with any existing contents of the host directory, leaving any existing files on the host that aren't in the exported directory alone.
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> ExportAsync(string path, bool? wipe = false, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path)));
        if (wipe is bool wipe_)
        {
            arguments = arguments.Add(new Argument("wipe", new BooleanValue(wipe_)));
        }

        var queryBuilder = QueryBuilder.Select("export", arguments);
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Retrieve a file at the given path.
    /// </summary>
    /// <param name = "path">
    /// Location of the file to retrieve (e.g., "README.md").
    /// </param>
    public File File(string path)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path)));
        var queryBuilder = QueryBuilder.Select("file", arguments);
        return new File(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Return a snapshot with some paths included or excluded
    /// </summary>
    /// <param name = "exclude">
    /// If set, paths matching one of these glob patterns is excluded from the new snapshot. Example: ["node_modules/", ".git*", ".env"]
    /// </param>
    /// <param name = "include">
    /// If set, only paths matching one of these glob patterns is included in the new snapshot. Example: (e.g., ["app/", "package.*"]).
    /// </param>
    /// <param name = "gitignore">
    /// If set, apply .gitignore rules when filtering the directory.
    /// </param>
    public Directory Filter(string[]? exclude = null, string[]? include = null, bool? gitignore = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (exclude is string[] exclude_)
        {
            arguments = arguments.Add(new Argument("exclude", new ListValue(exclude_.Select(v => new StringValue(v) as Value).ToList())));
        }

        if (include is string[] include_)
        {
            arguments = arguments.Add(new Argument("include", new ListValue(include_.Select(v => new StringValue(v) as Value).ToList())));
        }

        if (gitignore is bool gitignore_)
        {
            arguments = arguments.Add(new Argument("gitignore", new BooleanValue(gitignore_)));
        }

        var queryBuilder = QueryBuilder.Select("filter", arguments);
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Search up the directory tree for a file or directory, and return its path. If no match, return null
    /// </summary>
    /// <param name = "name">
    /// The name of the file or directory to search for
    /// </param>
    /// <param name = "start">
    /// The path to start the search from
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> FindUpAsync(string name, string start, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("start", new StringValue(start)));
        var queryBuilder = QueryBuilder.Select("findUp", arguments);
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Returns a list of files and directories that matche the given pattern.
    /// </summary>
    /// <param name = "pattern">
    /// Pattern to match (e.g., "*.md").
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string[]> GlobAsync(string pattern, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("pattern", new StringValue(pattern)));
        var queryBuilder = QueryBuilder.Select("glob", arguments);
        return await QueryExecutor.ExecuteListAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A unique identifier for this Directory.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<DirectoryId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<DirectoryId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Returns the name of the directory.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> NameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("name");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Searches for content matching the given regular expression or literal string.
    ///
    /// Uses Rust regex syntax; escape literal ., [, ], {, }, | with backslashes.
    /// </summary>
    /// <param name = "paths">
    /// Directory or file paths to search
    /// </param>
    /// <param name = "globs">
    /// Glob patterns to match (e.g., "*.md")
    /// </param>
    /// <param name = "pattern">
    /// The text to match.
    /// </param>
    /// <param name = "literal">
    /// Interpret the pattern as a literal string instead of a regular expression.
    /// </param>
    /// <param name = "multiline">
    /// Enable searching across multiple lines.
    /// </param>
    /// <param name = "dotall">
    /// Allow the . pattern to match newlines in multiline mode.
    /// </param>
    /// <param name = "insensitive">
    /// Enable case-insensitive matching.
    /// </param>
    /// <param name = "skipIgnored">
    /// Honor .gitignore, .ignore, and .rgignore files.
    /// </param>
    /// <param name = "skipHidden">
    /// Skip hidden files (files starting with .).
    /// </param>
    /// <param name = "filesOnly">
    /// Only return matching files, not lines and content
    /// </param>
    /// <param name = "limit">
    /// Limit the number of results to return
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<SearchResult[]> SearchAsync(string pattern, string[]? paths = null, string[]? globs = null, bool? literal = false, bool? multiline = false, bool? dotall = false, bool? insensitive = false, bool? skipIgnored = false, bool? skipHidden = false, bool? filesOnly = false, int? limit = null, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("pattern", new StringValue(pattern)));
        if (paths is string[] paths_)
        {
            arguments = arguments.Add(new Argument("paths", new ListValue(paths_.Select(v => new StringValue(v) as Value).ToList())));
        }

        if (globs is string[] globs_)
        {
            arguments = arguments.Add(new Argument("globs", new ListValue(globs_.Select(v => new StringValue(v) as Value).ToList())));
        }

        if (literal is bool literal_)
        {
            arguments = arguments.Add(new Argument("literal", new BooleanValue(literal_)));
        }

        if (multiline is bool multiline_)
        {
            arguments = arguments.Add(new Argument("multiline", new BooleanValue(multiline_)));
        }

        if (dotall is bool dotall_)
        {
            arguments = arguments.Add(new Argument("dotall", new BooleanValue(dotall_)));
        }

        if (insensitive is bool insensitive_)
        {
            arguments = arguments.Add(new Argument("insensitive", new BooleanValue(insensitive_)));
        }

        if (skipIgnored is bool skipIgnored_)
        {
            arguments = arguments.Add(new Argument("skipIgnored", new BooleanValue(skipIgnored_)));
        }

        if (skipHidden is bool skipHidden_)
        {
            arguments = arguments.Add(new Argument("skipHidden", new BooleanValue(skipHidden_)));
        }

        if (filesOnly is bool filesOnly_)
        {
            arguments = arguments.Add(new Argument("filesOnly", new BooleanValue(filesOnly_)));
        }

        if (limit is int limit_)
        {
            arguments = arguments.Add(new Argument("limit", new IntValue(limit_)));
        }

        var queryBuilder = QueryBuilder.Select("search", arguments).Select("id");
        return (await QueryExecutor.ExecuteListAsync<SearchResultId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new SearchResult(QueryBuilder.Builder().Select("loadSearchResultFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// Force evaluation in the engine.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<DirectoryId> SyncAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("sync");
        return await QueryExecutor.ExecuteAsync<DirectoryId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Opens an interactive terminal in new container with this directory mounted inside.
    /// </summary>
    /// <param name = "container">
    /// If set, override the default container used for the terminal.
    /// </param>
    /// <param name = "cmd">
    /// If set, override the container's default terminal command and invoke these command arguments instead.
    /// </param>
    /// <param name = "experimentalPrivilegedNesting">
    /// Provides Dagger access to the executed command.
    /// </param>
    /// <param name = "insecureRootCapabilities">
    /// Execute the command with all root capabilities. This is similar to running a command with "sudo" or executing "docker run" with the "--privileged" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.
    /// </param>
    public Directory Terminal(Container? container = null, string[]? cmd = null, bool? experimentalPrivilegedNesting = false, bool? insecureRootCapabilities = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (container is Container container_)
        {
            arguments = arguments.Add(new Argument("container", new IdValue<ContainerId>(container_)));
        }

        if (cmd is string[] cmd_)
        {
            arguments = arguments.Add(new Argument("cmd", new ListValue(cmd_.Select(v => new StringValue(v) as Value).ToList())));
        }

        if (experimentalPrivilegedNesting is bool experimentalPrivilegedNesting_)
        {
            arguments = arguments.Add(new Argument("experimentalPrivilegedNesting", new BooleanValue(experimentalPrivilegedNesting_)));
        }

        if (insecureRootCapabilities is bool insecureRootCapabilities_)
        {
            arguments = arguments.Add(new Argument("insecureRootCapabilities", new BooleanValue(insecureRootCapabilities_)));
        }

        var queryBuilder = QueryBuilder.Select("terminal", arguments);
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Return a directory with changes from another directory applied to it.
    /// </summary>
    /// <param name = "changes">
    /// Changes to apply to the directory
    /// </param>
    public Directory WithChanges(Changeset changes)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("changes", new IdValue<ChangesetId>(changes)));
        var queryBuilder = QueryBuilder.Select("withChanges", arguments);
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Return a snapshot with a directory added
    /// </summary>
    /// <param name = "path">
    /// Location of the written directory (e.g., "/src/").
    /// </param>
    /// <param name = "source">
    /// Identifier of the directory to copy.
    /// </param>
    /// <param name = "exclude">
    /// Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).
    /// </param>
    /// <param name = "include">
    /// Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).
    /// </param>
    /// <param name = "gitignore">
    /// Apply .gitignore filter rules inside the directory
    /// </param>
    /// <param name = "owner">
    /// A user:group to set for the copied directory and its contents.
    /// 
    /// The user and group must be an ID (1000:1000), not a name (foo:bar).
    /// 
    /// If the group is omitted, it defaults to the same as the user.
    /// </param>
    public Directory WithDirectory(string path, Directory source, string[]? exclude = null, string[]? include = null, bool? gitignore = false, string? owner = null)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path))).Add(new Argument("source", new IdValue<DirectoryId>(source)));
        if (exclude is string[] exclude_)
        {
            arguments = arguments.Add(new Argument("exclude", new ListValue(exclude_.Select(v => new StringValue(v) as Value).ToList())));
        }

        if (include is string[] include_)
        {
            arguments = arguments.Add(new Argument("include", new ListValue(include_.Select(v => new StringValue(v) as Value).ToList())));
        }

        if (gitignore is bool gitignore_)
        {
            arguments = arguments.Add(new Argument("gitignore", new BooleanValue(gitignore_)));
        }

        if (owner is string owner_)
        {
            arguments = arguments.Add(new Argument("owner", new StringValue(owner_)));
        }

        var queryBuilder = QueryBuilder.Select("withDirectory", arguments);
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Raise an error.
    /// </summary>
    /// <param name = "err">
    /// Message of the error to raise. If empty, the error will be ignored.
    /// </param>
    public Directory WithError(string err)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("err", new StringValue(err)));
        var queryBuilder = QueryBuilder.Select("withError", arguments);
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves this directory plus the contents of the given file copied to the given path.
    /// </summary>
    /// <param name = "path">
    /// Location of the copied file (e.g., "/file.txt").
    /// </param>
    /// <param name = "source">
    /// Identifier of the file to copy.
    /// </param>
    /// <param name = "permissions">
    /// Permission given to the copied file (e.g., 0600).
    /// </param>
    /// <param name = "owner">
    /// A user:group to set for the copied directory and its contents.
    /// 
    /// The user and group must be an ID (1000:1000), not a name (foo:bar).
    /// 
    /// If the group is omitted, it defaults to the same as the user.
    /// </param>
    public Directory WithFile(string path, File source, int? permissions = null, string? owner = null)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path))).Add(new Argument("source", new IdValue<FileId>(source)));
        if (permissions is int permissions_)
        {
            arguments = arguments.Add(new Argument("permissions", new IntValue(permissions_)));
        }

        if (owner is string owner_)
        {
            arguments = arguments.Add(new Argument("owner", new StringValue(owner_)));
        }

        var queryBuilder = QueryBuilder.Select("withFile", arguments);
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves this directory plus the contents of the given files copied to the given path.
    /// </summary>
    /// <param name = "path">
    /// Location where copied files should be placed (e.g., "/src").
    /// </param>
    /// <param name = "sources">
    /// Identifiers of the files to copy.
    /// </param>
    /// <param name = "permissions">
    /// Permission given to the copied files (e.g., 0600).
    /// </param>
    public Directory WithFiles(string path, File[] sources, int? permissions = null)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path))).Add(new Argument("sources", new ListValue(sources.Select(v => new IdValue<FileId>(v) as Value).ToList())));
        if (permissions is int permissions_)
        {
            arguments = arguments.Add(new Argument("permissions", new IntValue(permissions_)));
        }

        var queryBuilder = QueryBuilder.Select("withFiles", arguments);
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves this directory plus a new directory created at the given path.
    /// </summary>
    /// <param name = "path">
    /// Location of the directory created (e.g., "/logs").
    /// </param>
    /// <param name = "permissions">
    /// Permission granted to the created directory (e.g., 0777).
    /// </param>
    public Directory WithNewDirectory(string path, int? permissions = 420)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path)));
        if (permissions is int permissions_)
        {
            arguments = arguments.Add(new Argument("permissions", new IntValue(permissions_)));
        }

        var queryBuilder = QueryBuilder.Select("withNewDirectory", arguments);
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Return a snapshot with a new file added
    /// </summary>
    /// <param name = "path">
    /// Path of the new file. Example: "foo/bar.txt"
    /// </param>
    /// <param name = "contents">
    /// Contents of the new file. Example: "Hello world!"
    /// </param>
    /// <param name = "permissions">
    /// Permissions of the new file. Example: 0600
    /// </param>
    public Directory WithNewFile(string path, string contents, int? permissions = 420)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path))).Add(new Argument("contents", new StringValue(contents)));
        if (permissions is int permissions_)
        {
            arguments = arguments.Add(new Argument("permissions", new IntValue(permissions_)));
        }

        var queryBuilder = QueryBuilder.Select("withNewFile", arguments);
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves this directory with the given Git-compatible patch applied.
    /// </summary>
    /// <param name = "patch">
    /// Patch to apply (e.g., "diff --git a/file.txt b/file.txt\nindex 1234567..abcdef8 100644\n--- a/file.txt\n+++ b/file.txt\n@@ -1,1 +1,1 @@\n-Hello\n+World\n").
    /// </param>
    public Directory WithPatch(string patch)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("patch", new StringValue(patch)));
        var queryBuilder = QueryBuilder.Select("withPatch", arguments);
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves this directory with the given Git-compatible patch file applied.
    /// </summary>
    /// <param name = "patch">
    /// File containing the patch to apply
    /// </param>
    public Directory WithPatchFile(File patch)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("patch", new IdValue<FileId>(patch)));
        var queryBuilder = QueryBuilder.Select("withPatchFile", arguments);
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Return a snapshot with a symlink
    /// </summary>
    /// <param name = "target">
    /// Location of the file or directory to link to (e.g., "/existing/file").
    /// </param>
    /// <param name = "linkName">
    /// Location where the symbolic link will be created (e.g., "/new-file-link").
    /// </param>
    public Directory WithSymlink(string target, string linkName)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("target", new StringValue(target))).Add(new Argument("linkName", new StringValue(linkName)));
        var queryBuilder = QueryBuilder.Select("withSymlink", arguments);
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves this directory with all file/dir timestamps set to the given time.
    /// </summary>
    /// <param name = "timestamp">
    /// Timestamp to set dir/files in.
    /// 
    /// Formatted in seconds following Unix epoch (e.g., 1672531199).
    /// </param>
    public Directory WithTimestamps(int timestamp)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("timestamp", new IntValue(timestamp)));
        var queryBuilder = QueryBuilder.Select("withTimestamps", arguments);
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Return a snapshot with a subdirectory removed
    /// </summary>
    /// <param name = "path">
    /// Path of the subdirectory to remove. Example: ".github/workflows"
    /// </param>
    public Directory WithoutDirectory(string path)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path)));
        var queryBuilder = QueryBuilder.Select("withoutDirectory", arguments);
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Return a snapshot with a file removed
    /// </summary>
    /// <param name = "path">
    /// Path of the file to remove (e.g., "/file.txt").
    /// </param>
    public Directory WithoutFile(string path)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path)));
        var queryBuilder = QueryBuilder.Select("withoutFile", arguments);
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Return a snapshot with files removed
    /// </summary>
    /// <param name = "paths">
    /// Paths of the files to remove (e.g., ["/file.txt"]).
    /// </param>
    public Directory WithoutFiles(string[] paths)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("paths", new ListValue(paths.Select(v => new StringValue(v) as Value).ToList())));
        var queryBuilder = QueryBuilder.Select("withoutFiles", arguments);
        return new Directory(queryBuilder, GraphQLClient);
    }
}

/// <summary>
/// The `DirectoryID` scalar type represents an identifier for an object of type Directory.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<DirectoryId>))]
public class DirectoryId : Scalar
{
}

/// <summary>
/// A definition of a custom enum defined in a Module.
/// </summary>
public class EnumTypeDef(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<EnumTypeDefId>
{
    /// <summary>
    /// A doc string for the enum, if any.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> DescriptionAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("description");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A unique identifier for this EnumTypeDef.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<EnumTypeDefId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<EnumTypeDefId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The members of the enum.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<EnumValueTypeDef[]> MembersAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("members").Select("id");
        return (await QueryExecutor.ExecuteListAsync<EnumValueTypeDefId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new EnumValueTypeDef(QueryBuilder.Builder().Select("loadEnumValueTypeDefFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// The name of the enum.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> NameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("name");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The location of this enum declaration.
    /// </summary>
    public SourceMap SourceMap()
    {
        var queryBuilder = QueryBuilder.Select("sourceMap");
        return new SourceMap(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// If this EnumTypeDef is associated with a Module, the name of the module. Unset otherwise.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> SourceModuleNameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("sourceModuleName");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// values
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    [Obsolete("use members instead")]
    public async Task<EnumValueTypeDef[]> ValuesAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("values").Select("id");
        return (await QueryExecutor.ExecuteListAsync<EnumValueTypeDefId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new EnumValueTypeDef(QueryBuilder.Builder().Select("loadEnumValueTypeDefFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }
}

/// <summary>
/// The `EnumTypeDefID` scalar type represents an identifier for an object of type EnumTypeDef.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<EnumTypeDefId>))]
public class EnumTypeDefId : Scalar
{
}

/// <summary>
/// A definition of a value in a custom enum defined in a Module.
/// </summary>
public class EnumValueTypeDef(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<EnumValueTypeDefId>
{
    /// <summary>
    /// The reason this enum member is deprecated, if any.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> DeprecatedAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("deprecated");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A doc string for the enum member, if any.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> DescriptionAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("description");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A unique identifier for this EnumValueTypeDef.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<EnumValueTypeDefId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<EnumValueTypeDefId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The name of the enum member.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> NameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("name");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The location of this enum member declaration.
    /// </summary>
    public SourceMap SourceMap()
    {
        var queryBuilder = QueryBuilder.Select("sourceMap");
        return new SourceMap(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// The value of the enum member
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> ValueAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("value");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// The `EnumValueTypeDefID` scalar type represents an identifier for an object of type EnumValueTypeDef.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<EnumValueTypeDefId>))]
public class EnumValueTypeDefId : Scalar
{
}

/// <summary>
/// Env
/// </summary>
public class Env(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<EnvId>
{
    /// <summary>
    /// A unique identifier for this Env.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<EnvId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<EnvId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Retrieves an input binding by name
    /// </summary>
    /// <param name = "name">
    /// 
    /// </param>
    public Binding Input(string name)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name)));
        var queryBuilder = QueryBuilder.Select("input", arguments);
        return new Binding(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Returns all input bindings provided to the environment
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Binding[]> InputsAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("inputs").Select("id");
        return (await QueryExecutor.ExecuteListAsync<BindingId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new Binding(QueryBuilder.Builder().Select("loadBindingFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// Retrieves an output binding by name
    /// </summary>
    /// <param name = "name">
    /// 
    /// </param>
    public Binding Output(string name)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name)));
        var queryBuilder = QueryBuilder.Select("output", arguments);
        return new Binding(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Returns all declared output bindings for the environment
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Binding[]> OutputsAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("outputs").Select("id");
        return (await QueryExecutor.ExecuteListAsync<BindingId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new Binding(QueryBuilder.Builder().Select("loadBindingFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// Create or update a binding of type Address in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "value">
    /// The Address value to assign to the binding
    /// </param>
    /// <param name = "description">
    /// The purpose of the input
    /// </param>
    public Env WithAddressInput(string name, Address value, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new IdValue<AddressId>(value))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withAddressInput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Declare a desired Address output to be assigned in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "description">
    /// A description of the desired value of the binding
    /// </param>
    public Env WithAddressOutput(string name, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withAddressOutput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Create or update a binding of type CacheVolume in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "value">
    /// The CacheVolume value to assign to the binding
    /// </param>
    /// <param name = "description">
    /// The purpose of the input
    /// </param>
    public Env WithCacheVolumeInput(string name, CacheVolume value, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new IdValue<CacheVolumeId>(value))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withCacheVolumeInput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Declare a desired CacheVolume output to be assigned in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "description">
    /// A description of the desired value of the binding
    /// </param>
    public Env WithCacheVolumeOutput(string name, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withCacheVolumeOutput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Create or update a binding of type Changeset in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "value">
    /// The Changeset value to assign to the binding
    /// </param>
    /// <param name = "description">
    /// The purpose of the input
    /// </param>
    public Env WithChangesetInput(string name, Changeset value, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new IdValue<ChangesetId>(value))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withChangesetInput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Declare a desired Changeset output to be assigned in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "description">
    /// A description of the desired value of the binding
    /// </param>
    public Env WithChangesetOutput(string name, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withChangesetOutput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Create or update a binding of type CheckGroup in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "value">
    /// The CheckGroup value to assign to the binding
    /// </param>
    /// <param name = "description">
    /// The purpose of the input
    /// </param>
    public Env WithCheckGroupInput(string name, CheckGroup value, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new IdValue<CheckGroupId>(value))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withCheckGroupInput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Declare a desired CheckGroup output to be assigned in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "description">
    /// A description of the desired value of the binding
    /// </param>
    public Env WithCheckGroupOutput(string name, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withCheckGroupOutput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Create or update a binding of type Check in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "value">
    /// The Check value to assign to the binding
    /// </param>
    /// <param name = "description">
    /// The purpose of the input
    /// </param>
    public Env WithCheckInput(string name, Check value, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new IdValue<CheckId>(value))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withCheckInput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Declare a desired Check output to be assigned in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "description">
    /// A description of the desired value of the binding
    /// </param>
    public Env WithCheckOutput(string name, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withCheckOutput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Create or update a binding of type Cloud in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "value">
    /// The Cloud value to assign to the binding
    /// </param>
    /// <param name = "description">
    /// The purpose of the input
    /// </param>
    public Env WithCloudInput(string name, Cloud value, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new IdValue<CloudId>(value))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withCloudInput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Declare a desired Cloud output to be assigned in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "description">
    /// A description of the desired value of the binding
    /// </param>
    public Env WithCloudOutput(string name, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withCloudOutput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Create or update a binding of type Container in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "value">
    /// The Container value to assign to the binding
    /// </param>
    /// <param name = "description">
    /// The purpose of the input
    /// </param>
    public Env WithContainerInput(string name, Container value, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new IdValue<ContainerId>(value))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withContainerInput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Declare a desired Container output to be assigned in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "description">
    /// A description of the desired value of the binding
    /// </param>
    public Env WithContainerOutput(string name, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withContainerOutput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Installs the current module into the environment, exposing its functions to the model
    ///
    /// Contextual path arguments will be populated using the environment's workspace.
    /// </summary>
    public Env WithCurrentModule()
    {
        var queryBuilder = QueryBuilder.Select("withCurrentModule");
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Create or update a binding of type Directory in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "value">
    /// The Directory value to assign to the binding
    /// </param>
    /// <param name = "description">
    /// The purpose of the input
    /// </param>
    public Env WithDirectoryInput(string name, Directory value, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new IdValue<DirectoryId>(value))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withDirectoryInput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Declare a desired Directory output to be assigned in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "description">
    /// A description of the desired value of the binding
    /// </param>
    public Env WithDirectoryOutput(string name, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withDirectoryOutput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Create or update a binding of type EnvFile in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "value">
    /// The EnvFile value to assign to the binding
    /// </param>
    /// <param name = "description">
    /// The purpose of the input
    /// </param>
    public Env WithEnvFileInput(string name, EnvFile value, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new IdValue<EnvFileId>(value))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withEnvFileInput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Declare a desired EnvFile output to be assigned in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "description">
    /// A description of the desired value of the binding
    /// </param>
    public Env WithEnvFileOutput(string name, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withEnvFileOutput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Create or update a binding of type Env in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "value">
    /// The Env value to assign to the binding
    /// </param>
    /// <param name = "description">
    /// The purpose of the input
    /// </param>
    public Env WithEnvInput(string name, Env value, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new IdValue<EnvId>(value))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withEnvInput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Declare a desired Env output to be assigned in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "description">
    /// A description of the desired value of the binding
    /// </param>
    public Env WithEnvOutput(string name, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withEnvOutput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Create or update a binding of type File in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "value">
    /// The File value to assign to the binding
    /// </param>
    /// <param name = "description">
    /// The purpose of the input
    /// </param>
    public Env WithFileInput(string name, File value, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new IdValue<FileId>(value))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withFileInput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Declare a desired File output to be assigned in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "description">
    /// A description of the desired value of the binding
    /// </param>
    public Env WithFileOutput(string name, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withFileOutput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Create or update a binding of type GitRef in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "value">
    /// The GitRef value to assign to the binding
    /// </param>
    /// <param name = "description">
    /// The purpose of the input
    /// </param>
    public Env WithGitRefInput(string name, GitRef value, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new IdValue<GitRefId>(value))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withGitRefInput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Declare a desired GitRef output to be assigned in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "description">
    /// A description of the desired value of the binding
    /// </param>
    public Env WithGitRefOutput(string name, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withGitRefOutput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Create or update a binding of type GitRepository in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "value">
    /// The GitRepository value to assign to the binding
    /// </param>
    /// <param name = "description">
    /// The purpose of the input
    /// </param>
    public Env WithGitRepositoryInput(string name, GitRepository value, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new IdValue<GitRepositoryId>(value))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withGitRepositoryInput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Declare a desired GitRepository output to be assigned in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "description">
    /// A description of the desired value of the binding
    /// </param>
    public Env WithGitRepositoryOutput(string name, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withGitRepositoryOutput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Create or update a binding of type JSONValue in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "value">
    /// The JSONValue value to assign to the binding
    /// </param>
    /// <param name = "description">
    /// The purpose of the input
    /// </param>
    public Env WithJsonvalueInput(string name, Jsonvalue value, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new IdValue<JsonvalueId>(value))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withJSONValueInput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Declare a desired JSONValue output to be assigned in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "description">
    /// A description of the desired value of the binding
    /// </param>
    public Env WithJsonvalueOutput(string name, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withJSONValueOutput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Installs a module into the environment, exposing its functions to the model
    ///
    /// Contextual path arguments will be populated using the environment's workspace.
    /// </summary>
    /// <param name = "module">
    /// 
    /// </param>
    public Env WithModule(Module module)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("module", new IdValue<ModuleId>(module)));
        var queryBuilder = QueryBuilder.Select("withModule", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Create or update a binding of type ModuleConfigClient in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "value">
    /// The ModuleConfigClient value to assign to the binding
    /// </param>
    /// <param name = "description">
    /// The purpose of the input
    /// </param>
    public Env WithModuleConfigClientInput(string name, ModuleConfigClient value, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new IdValue<ModuleConfigClientId>(value))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withModuleConfigClientInput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Declare a desired ModuleConfigClient output to be assigned in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "description">
    /// A description of the desired value of the binding
    /// </param>
    public Env WithModuleConfigClientOutput(string name, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withModuleConfigClientOutput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Create or update a binding of type Module in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "value">
    /// The Module value to assign to the binding
    /// </param>
    /// <param name = "description">
    /// The purpose of the input
    /// </param>
    public Env WithModuleInput(string name, Module value, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new IdValue<ModuleId>(value))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withModuleInput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Declare a desired Module output to be assigned in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "description">
    /// A description of the desired value of the binding
    /// </param>
    public Env WithModuleOutput(string name, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withModuleOutput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Create or update a binding of type ModuleSource in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "value">
    /// The ModuleSource value to assign to the binding
    /// </param>
    /// <param name = "description">
    /// The purpose of the input
    /// </param>
    public Env WithModuleSourceInput(string name, ModuleSource value, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new IdValue<ModuleSourceId>(value))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withModuleSourceInput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Declare a desired ModuleSource output to be assigned in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "description">
    /// A description of the desired value of the binding
    /// </param>
    public Env WithModuleSourceOutput(string name, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withModuleSourceOutput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Create or update a binding of type SearchResult in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "value">
    /// The SearchResult value to assign to the binding
    /// </param>
    /// <param name = "description">
    /// The purpose of the input
    /// </param>
    public Env WithSearchResultInput(string name, SearchResult value, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new IdValue<SearchResultId>(value))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withSearchResultInput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Declare a desired SearchResult output to be assigned in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "description">
    /// A description of the desired value of the binding
    /// </param>
    public Env WithSearchResultOutput(string name, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withSearchResultOutput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Create or update a binding of type SearchSubmatch in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "value">
    /// The SearchSubmatch value to assign to the binding
    /// </param>
    /// <param name = "description">
    /// The purpose of the input
    /// </param>
    public Env WithSearchSubmatchInput(string name, SearchSubmatch value, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new IdValue<SearchSubmatchId>(value))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withSearchSubmatchInput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Declare a desired SearchSubmatch output to be assigned in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "description">
    /// A description of the desired value of the binding
    /// </param>
    public Env WithSearchSubmatchOutput(string name, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withSearchSubmatchOutput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Create or update a binding of type Secret in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "value">
    /// The Secret value to assign to the binding
    /// </param>
    /// <param name = "description">
    /// The purpose of the input
    /// </param>
    public Env WithSecretInput(string name, Secret value, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new IdValue<SecretId>(value))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withSecretInput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Declare a desired Secret output to be assigned in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "description">
    /// A description of the desired value of the binding
    /// </param>
    public Env WithSecretOutput(string name, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withSecretOutput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Create or update a binding of type Service in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "value">
    /// The Service value to assign to the binding
    /// </param>
    /// <param name = "description">
    /// The purpose of the input
    /// </param>
    public Env WithServiceInput(string name, Service value, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new IdValue<ServiceId>(value))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withServiceInput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Declare a desired Service output to be assigned in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "description">
    /// A description of the desired value of the binding
    /// </param>
    public Env WithServiceOutput(string name, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withServiceOutput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Create or update a binding of type Socket in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "value">
    /// The Socket value to assign to the binding
    /// </param>
    /// <param name = "description">
    /// The purpose of the input
    /// </param>
    public Env WithSocketInput(string name, Socket value, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new IdValue<SocketId>(value))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withSocketInput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Declare a desired Socket output to be assigned in the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "description">
    /// A description of the desired value of the binding
    /// </param>
    public Env WithSocketOutput(string name, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withSocketOutput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Provides a string input binding to the environment
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "value">
    /// The string value to assign to the binding
    /// </param>
    /// <param name = "description">
    /// The description of the input
    /// </param>
    public Env WithStringInput(string name, string value, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new StringValue(value))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withStringInput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Declares a desired string output binding
    /// </summary>
    /// <param name = "name">
    /// The name of the binding
    /// </param>
    /// <param name = "description">
    /// The description of the output
    /// </param>
    public Env WithStringOutput(string name, string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withStringOutput", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Returns a new environment with the provided workspace
    /// </summary>
    /// <param name = "workspace">
    /// The directory to set as the host filesystem
    /// </param>
    public Env WithWorkspace(Directory workspace)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("workspace", new IdValue<DirectoryId>(workspace)));
        var queryBuilder = QueryBuilder.Select("withWorkspace", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Returns a new environment without any outputs
    /// </summary>
    public Env WithoutOutputs()
    {
        var queryBuilder = QueryBuilder.Select("withoutOutputs");
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// workspace
    /// </summary>
    public Directory Workspace()
    {
        var queryBuilder = QueryBuilder.Select("workspace");
        return new Directory(queryBuilder, GraphQLClient);
    }
}

/// <summary>
/// A collection of environment variables.
/// </summary>
public class EnvFile(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<EnvFileId>
{
    /// <summary>
    /// Return as a file
    /// </summary>
    public File AsFile()
    {
        var queryBuilder = QueryBuilder.Select("asFile");
        return new File(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Check if a variable exists
    /// </summary>
    /// <param name = "name">
    /// Variable name
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<bool> ExistsAsync(string name, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name)));
        var queryBuilder = QueryBuilder.Select("exists", arguments);
        return await QueryExecutor.ExecuteAsync<bool>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Lookup a variable (last occurrence wins) and return its value, or an empty string
    /// </summary>
    /// <param name = "name">
    /// Variable name
    /// </param>
    /// <param name = "raw">
    /// Return the value exactly as written to the file. No quote removal or variable expansion
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> GetAsync(string name, bool? raw = null, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name)));
        if (raw is bool raw_)
        {
            arguments = arguments.Add(new Argument("raw", new BooleanValue(raw_)));
        }

        var queryBuilder = QueryBuilder.Select("get", arguments);
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A unique identifier for this EnvFile.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<EnvFileId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<EnvFileId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Filters variables by prefix and removes the pref from keys. Variables without the prefix are excluded. For example, with the prefix "MY_APP_" and variables: MY_APP_TOKEN=topsecret MY_APP_NAME=hello FOO=bar the resulting environment will contain: TOKEN=topsecret NAME=hello
    /// </summary>
    /// <param name = "prefix">
    /// The prefix to filter by
    /// </param>
    public EnvFile Namespace(string prefix)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("prefix", new StringValue(prefix)));
        var queryBuilder = QueryBuilder.Select("namespace", arguments);
        return new EnvFile(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Return all variables
    /// </summary>
    /// <param name = "raw">
    /// Return values exactly as written to the file. No quote removal or variable expansion
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<EnvVariable[]> VariablesAsync(bool? raw = null, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (raw is bool raw_)
        {
            arguments = arguments.Add(new Argument("raw", new BooleanValue(raw_)));
        }

        var queryBuilder = QueryBuilder.Select("variables", arguments).Select("id");
        return (await QueryExecutor.ExecuteListAsync<EnvVariableId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new EnvVariable(QueryBuilder.Builder().Select("loadEnvVariableFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// Add a variable
    /// </summary>
    /// <param name = "name">
    /// Variable name
    /// </param>
    /// <param name = "value">
    /// Variable value
    /// </param>
    public EnvFile WithVariable(string name, string value)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new StringValue(value)));
        var queryBuilder = QueryBuilder.Select("withVariable", arguments);
        return new EnvFile(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Remove all occurrences of the named variable
    /// </summary>
    /// <param name = "name">
    /// Variable name
    /// </param>
    public EnvFile WithoutVariable(string name)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name)));
        var queryBuilder = QueryBuilder.Select("withoutVariable", arguments);
        return new EnvFile(queryBuilder, GraphQLClient);
    }
}

/// <summary>
/// The `EnvFileID` scalar type represents an identifier for an object of type EnvFile.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<EnvFileId>))]
public class EnvFileId : Scalar
{
}

/// <summary>
/// The `EnvID` scalar type represents an identifier for an object of type Env.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<EnvId>))]
public class EnvId : Scalar
{
}

/// <summary>
/// An environment variable name and value.
/// </summary>
public class EnvVariable(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<EnvVariableId>
{
    /// <summary>
    /// A unique identifier for this EnvVariable.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<EnvVariableId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<EnvVariableId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The environment variable name.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> NameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("name");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The environment variable value.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> ValueAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("value");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// The `EnvVariableID` scalar type represents an identifier for an object of type EnvVariable.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<EnvVariableId>))]
public class EnvVariableId : Scalar
{
}

/// <summary>
/// Error
/// </summary>
public class Error(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<ErrorId>
{
    /// <summary>
    /// A unique identifier for this Error.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<ErrorId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<ErrorId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A description of the error.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> MessageAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("message");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The extensions of the error.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<ErrorValue[]> ValuesAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("values").Select("id");
        return (await QueryExecutor.ExecuteListAsync<ErrorValueId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new ErrorValue(QueryBuilder.Builder().Select("loadErrorValueFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// Add a value to the error.
    /// </summary>
    /// <param name = "name">
    /// The name of the value.
    /// </param>
    /// <param name = "value">
    /// The value to store on the error.
    /// </param>
    public Error WithValue(string name, Json value)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("value", new StringValue(value.Value)));
        var queryBuilder = QueryBuilder.Select("withValue", arguments);
        return new Error(queryBuilder, GraphQLClient);
    }
}

/// <summary>
/// The `ErrorID` scalar type represents an identifier for an object of type Error.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<ErrorId>))]
public class ErrorId : Scalar
{
}

/// <summary>
/// ErrorValue
/// </summary>
public class ErrorValue(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<ErrorValueId>
{
    /// <summary>
    /// A unique identifier for this ErrorValue.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<ErrorValueId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<ErrorValueId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The name of the value.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> NameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("name");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The value.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Json> ValueAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("value");
        return await QueryExecutor.ExecuteAsync<Json>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// The `ErrorValueID` scalar type represents an identifier for an object of type ErrorValue.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<ErrorValueId>))]
public class ErrorValueId : Scalar
{
}

/// <summary>
/// File type.
/// </summary>
[JsonConverter(typeof(JsonStringEnumConverter<ExistsType>))]
public enum ExistsType
{
    /// <summary>
    /// Tests path is a regular file
    /// </summary>
    REGULAR_TYPE,
    /// <summary>
    /// Tests path is a directory
    /// </summary>
    DIRECTORY_TYPE,
    /// <summary>
    /// Tests path is a symlink
    /// </summary>
    SYMLINK_TYPE
}

/// <summary>
/// A definition of a field on a custom object defined in a Module.
///
/// A field on an object has a static value, as opposed to a function on an object whose value is computed by invoking code (and can accept arguments).
/// </summary>
public class FieldTypeDef(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<FieldTypeDefId>
{
    /// <summary>
    /// The reason this enum member is deprecated, if any.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> DeprecatedAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("deprecated");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A doc string for the field, if any.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> DescriptionAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("description");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A unique identifier for this FieldTypeDef.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<FieldTypeDefId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<FieldTypeDefId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The name of the field in lowerCamelCase format.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> NameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("name");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The location of this field declaration.
    /// </summary>
    public SourceMap SourceMap()
    {
        var queryBuilder = QueryBuilder.Select("sourceMap");
        return new SourceMap(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// The type of the field.
    /// </summary>
    public TypeDef TypeDef()
    {
        var queryBuilder = QueryBuilder.Select("typeDef");
        return new TypeDef(queryBuilder, GraphQLClient);
    }
}

/// <summary>
/// The `FieldTypeDefID` scalar type represents an identifier for an object of type FieldTypeDef.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<FieldTypeDefId>))]
public class FieldTypeDefId : Scalar
{
}

/// <summary>
/// A file.
/// </summary>
public class File(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<FileId>
{
    /// <summary>
    /// Parse as an env file
    /// </summary>
    /// <param name = "expand">
    /// Replace "${VAR}" or "$VAR" with the value of other vars
    /// </param>
    public EnvFile AsEnvFile(bool? expand = null)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (expand is bool expand_)
        {
            arguments = arguments.Add(new Argument("expand", new BooleanValue(expand_)));
        }

        var queryBuilder = QueryBuilder.Select("asEnvFile", arguments);
        return new EnvFile(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Change the owner of the file recursively.
    /// </summary>
    /// <param name = "owner">
    /// A user:group to set for the file.
    /// 
    /// The user and group must be an ID (1000:1000), not a name (foo:bar).
    /// 
    /// If the group is omitted, it defaults to the same as the user.
    /// </param>
    public File Chown(string owner)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("owner", new StringValue(owner)));
        var queryBuilder = QueryBuilder.Select("chown", arguments);
        return new File(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves the contents of the file.
    /// </summary>
    /// <param name = "offsetLines">
    /// Start reading after this line
    /// </param>
    /// <param name = "limitLines">
    /// Maximum number of lines to read
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> ContentsAsync(int? offsetLines = null, int? limitLines = null, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (offsetLines is int offsetLines_)
        {
            arguments = arguments.Add(new Argument("offsetLines", new IntValue(offsetLines_)));
        }

        if (limitLines is int limitLines_)
        {
            arguments = arguments.Add(new Argument("limitLines", new IntValue(limitLines_)));
        }

        var queryBuilder = QueryBuilder.Select("contents", arguments);
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Return the file's digest. The format of the digest is not guaranteed to be stable between releases of Dagger. It is guaranteed to be stable between invocations of the same Dagger engine.
    /// </summary>
    /// <param name = "excludeMetadata">
    /// If true, exclude metadata from the digest.
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> DigestAsync(bool? excludeMetadata = false, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (excludeMetadata is bool excludeMetadata_)
        {
            arguments = arguments.Add(new Argument("excludeMetadata", new BooleanValue(excludeMetadata_)));
        }

        var queryBuilder = QueryBuilder.Select("digest", arguments);
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Writes the file to a file path on the host.
    /// </summary>
    /// <param name = "path">
    /// Location of the written directory (e.g., "output.txt").
    /// </param>
    /// <param name = "allowParentDirPath">
    /// If allowParentDirPath is true, the path argument can be a directory path, in which case the file will be created in that directory.
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> ExportAsync(string path, bool? allowParentDirPath = false, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path)));
        if (allowParentDirPath is bool allowParentDirPath_)
        {
            arguments = arguments.Add(new Argument("allowParentDirPath", new BooleanValue(allowParentDirPath_)));
        }

        var queryBuilder = QueryBuilder.Select("export", arguments);
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A unique identifier for this File.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<FileId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<FileId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Retrieves the name of the file.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> NameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("name");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Searches for content matching the given regular expression or literal string.
    ///
    /// Uses Rust regex syntax; escape literal ., [, ], {, }, | with backslashes.
    /// </summary>
    /// <param name = "pattern">
    /// The text to match.
    /// </param>
    /// <param name = "literal">
    /// Interpret the pattern as a literal string instead of a regular expression.
    /// </param>
    /// <param name = "multiline">
    /// Enable searching across multiple lines.
    /// </param>
    /// <param name = "dotall">
    /// Allow the . pattern to match newlines in multiline mode.
    /// </param>
    /// <param name = "insensitive">
    /// Enable case-insensitive matching.
    /// </param>
    /// <param name = "skipIgnored">
    /// Honor .gitignore, .ignore, and .rgignore files.
    /// </param>
    /// <param name = "skipHidden">
    /// Skip hidden files (files starting with .).
    /// </param>
    /// <param name = "filesOnly">
    /// Only return matching files, not lines and content
    /// </param>
    /// <param name = "limit">
    /// Limit the number of results to return
    /// </param>
    /// <param name = "paths">
    /// 
    /// </param>
    /// <param name = "globs">
    /// 
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<SearchResult[]> SearchAsync(string pattern, bool? literal = false, bool? multiline = false, bool? dotall = false, bool? insensitive = false, bool? skipIgnored = false, bool? skipHidden = false, bool? filesOnly = false, int? limit = null, string[]? paths = null, string[]? globs = null, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("pattern", new StringValue(pattern)));
        if (literal is bool literal_)
        {
            arguments = arguments.Add(new Argument("literal", new BooleanValue(literal_)));
        }

        if (multiline is bool multiline_)
        {
            arguments = arguments.Add(new Argument("multiline", new BooleanValue(multiline_)));
        }

        if (dotall is bool dotall_)
        {
            arguments = arguments.Add(new Argument("dotall", new BooleanValue(dotall_)));
        }

        if (insensitive is bool insensitive_)
        {
            arguments = arguments.Add(new Argument("insensitive", new BooleanValue(insensitive_)));
        }

        if (skipIgnored is bool skipIgnored_)
        {
            arguments = arguments.Add(new Argument("skipIgnored", new BooleanValue(skipIgnored_)));
        }

        if (skipHidden is bool skipHidden_)
        {
            arguments = arguments.Add(new Argument("skipHidden", new BooleanValue(skipHidden_)));
        }

        if (filesOnly is bool filesOnly_)
        {
            arguments = arguments.Add(new Argument("filesOnly", new BooleanValue(filesOnly_)));
        }

        if (limit is int limit_)
        {
            arguments = arguments.Add(new Argument("limit", new IntValue(limit_)));
        }

        if (paths is string[] paths_)
        {
            arguments = arguments.Add(new Argument("paths", new ListValue(paths_.Select(v => new StringValue(v) as Value).ToList())));
        }

        if (globs is string[] globs_)
        {
            arguments = arguments.Add(new Argument("globs", new ListValue(globs_.Select(v => new StringValue(v) as Value).ToList())));
        }

        var queryBuilder = QueryBuilder.Select("search", arguments).Select("id");
        return (await QueryExecutor.ExecuteListAsync<SearchResultId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new SearchResult(QueryBuilder.Builder().Select("loadSearchResultFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// Retrieves the size of the file, in bytes.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<int> SizeAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("size");
        return await QueryExecutor.ExecuteAsync<int>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Force evaluation in the engine.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<FileId> SyncAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("sync");
        return await QueryExecutor.ExecuteAsync<FileId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Retrieves this file with its name set to the given name.
    /// </summary>
    /// <param name = "name">
    /// Name to set file to.
    /// </param>
    public File WithName(string name)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name)));
        var queryBuilder = QueryBuilder.Select("withName", arguments);
        return new File(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves the file with content replaced with the given text.
    ///
    /// If 'all' is true, all occurrences of the pattern will be replaced.
    ///
    /// If 'firstAfter' is specified, only the first match starting at the specified line will be replaced.
    ///
    /// If neither are specified, and there are multiple matches for the pattern, this will error.
    ///
    /// If there are no matches for the pattern, this will error.
    /// </summary>
    /// <param name = "search">
    /// The text to match.
    /// </param>
    /// <param name = "replacement">
    /// The text to match.
    /// </param>
    /// <param name = "all">
    /// Replace all occurrences of the pattern.
    /// </param>
    /// <param name = "firstFrom">
    /// Replace the first match starting from the specified line.
    /// </param>
    public File WithReplaced(string search, string replacement, bool? all = false, int? firstFrom = null)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("search", new StringValue(search))).Add(new Argument("replacement", new StringValue(replacement)));
        if (all is bool all_)
        {
            arguments = arguments.Add(new Argument("all", new BooleanValue(all_)));
        }

        if (firstFrom is int firstFrom_)
        {
            arguments = arguments.Add(new Argument("firstFrom", new IntValue(firstFrom_)));
        }

        var queryBuilder = QueryBuilder.Select("withReplaced", arguments);
        return new File(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves this file with its created/modified timestamps set to the given time.
    /// </summary>
    /// <param name = "timestamp">
    /// Timestamp to set dir/files in.
    /// 
    /// Formatted in seconds following Unix epoch (e.g., 1672531199).
    /// </param>
    public File WithTimestamps(int timestamp)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("timestamp", new IntValue(timestamp)));
        var queryBuilder = QueryBuilder.Select("withTimestamps", arguments);
        return new File(queryBuilder, GraphQLClient);
    }
}

/// <summary>
/// The `FileID` scalar type represents an identifier for an object of type File.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<FileId>))]
public class FileId : Scalar
{
}

/// <summary>
/// Function represents a resolver provided by a Module.
///
/// A function always evaluates against a parent object and is given a set of named arguments.
/// </summary>
public class Function(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<FunctionId>
{
    /// <summary>
    /// Arguments accepted by the function, if any.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<FunctionArg[]> ArgsAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("args").Select("id");
        return (await QueryExecutor.ExecuteListAsync<FunctionArgId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new FunctionArg(QueryBuilder.Builder().Select("loadFunctionArgFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// The reason this function is deprecated, if any.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> DeprecatedAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("deprecated");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A doc string for the function, if any.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> DescriptionAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("description");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A unique identifier for this Function.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<FunctionId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<FunctionId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The name of the function.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> NameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("name");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The type returned by the function.
    /// </summary>
    public TypeDef ReturnType()
    {
        var queryBuilder = QueryBuilder.Select("returnType");
        return new TypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// The location of this function declaration.
    /// </summary>
    public SourceMap SourceMap()
    {
        var queryBuilder = QueryBuilder.Select("sourceMap");
        return new SourceMap(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Returns the function with the provided argument
    /// </summary>
    /// <param name = "name">
    /// The name of the argument
    /// </param>
    /// <param name = "typeDef">
    /// The type of the argument
    /// </param>
    /// <param name = "description">
    /// A doc string for the argument, if any
    /// </param>
    /// <param name = "defaultValue">
    /// A default value to use for this argument if not explicitly set by the caller, if any
    /// </param>
    /// <param name = "defaultPath">
    /// If the argument is a Directory or File type, default to load path from context directory, relative to root directory.
    /// </param>
    /// <param name = "ignore">
    /// Patterns to ignore when loading the contextual argument value.
    /// </param>
    /// <param name = "sourceMap">
    /// The source map for the argument definition.
    /// </param>
    /// <param name = "deprecated">
    /// If deprecated, the reason or migration path.
    /// </param>
    public Function WithArg(string name, TypeDef typeDef, string? description = null, Json? defaultValue = null, string? defaultPath = null, string[]? ignore = null, SourceMap? sourceMap = null, string? deprecated = null)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("typeDef", new IdValue<TypeDefId>(typeDef)));
        if (description is string description_)
        {
            arguments = arguments.Add(new Argument("description", new StringValue(description_)));
        }

        if (defaultValue is Json defaultValue_)
        {
            arguments = arguments.Add(new Argument("defaultValue", new StringValue(defaultValue_.Value)));
        }

        if (defaultPath is string defaultPath_)
        {
            arguments = arguments.Add(new Argument("defaultPath", new StringValue(defaultPath_)));
        }

        if (ignore is string[] ignore_)
        {
            arguments = arguments.Add(new Argument("ignore", new ListValue(ignore_.Select(v => new StringValue(v) as Value).ToList())));
        }

        if (sourceMap is SourceMap sourceMap_)
        {
            arguments = arguments.Add(new Argument("sourceMap", new IdValue<SourceMapId>(sourceMap_)));
        }

        if (deprecated is string deprecated_)
        {
            arguments = arguments.Add(new Argument("deprecated", new StringValue(deprecated_)));
        }

        var queryBuilder = QueryBuilder.Select("withArg", arguments);
        return new Function(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Returns the function updated to use the provided cache policy.
    /// </summary>
    /// <param name = "policy">
    /// The cache policy to use.
    /// </param>
    /// <param name = "timeToLive">
    /// The TTL for the cache policy, if applicable. Provided as a duration string, e.g. "5m", "1h30s".
    /// </param>
    public Function WithCachePolicy(FunctionCachePolicy policy, string? timeToLive = null)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("policy", new StringValue(policy.ToString())));
        if (timeToLive is string timeToLive_)
        {
            arguments = arguments.Add(new Argument("timeToLive", new StringValue(timeToLive_)));
        }

        var queryBuilder = QueryBuilder.Select("withCachePolicy", arguments);
        return new Function(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Returns the function with a flag indicating it's a check.
    /// </summary>
    public Function WithCheck()
    {
        var queryBuilder = QueryBuilder.Select("withCheck");
        return new Function(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Returns the function with the provided deprecation reason.
    /// </summary>
    /// <param name = "reason">
    /// Reason or migration path describing the deprecation.
    /// </param>
    public Function WithDeprecated(string? reason = null)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (reason is string reason_)
        {
            arguments = arguments.Add(new Argument("reason", new StringValue(reason_)));
        }

        var queryBuilder = QueryBuilder.Select("withDeprecated", arguments);
        return new Function(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Returns the function with the given doc string.
    /// </summary>
    /// <param name = "description">
    /// The doc string to set.
    /// </param>
    public Function WithDescription(string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withDescription", arguments);
        return new Function(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Returns the function with the given source map.
    /// </summary>
    /// <param name = "sourceMap">
    /// The source map for the function definition.
    /// </param>
    public Function WithSourceMap(SourceMap sourceMap)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("sourceMap", new IdValue<SourceMapId>(sourceMap)));
        var queryBuilder = QueryBuilder.Select("withSourceMap", arguments);
        return new Function(queryBuilder, GraphQLClient);
    }
}

/// <summary>
/// An argument accepted by a function.
///
/// This is a specification for an argument at function definition time, not an argument passed at function call time.
/// </summary>
public class FunctionArg(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<FunctionArgId>
{
    /// <summary>
    /// Only applies to arguments of type File or Directory. If the argument is not set, load it from the given path in the context directory
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> DefaultPathAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("defaultPath");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A default value to use for this argument when not explicitly set by the caller, if any.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Json> DefaultValueAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("defaultValue");
        return await QueryExecutor.ExecuteAsync<Json>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The reason this function is deprecated, if any.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> DeprecatedAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("deprecated");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A doc string for the argument, if any.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> DescriptionAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("description");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A unique identifier for this FunctionArg.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<FunctionArgId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<FunctionArgId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Only applies to arguments of type Directory. The ignore patterns are applied to the input directory, and matching entries are filtered out, in a cache-efficient manner.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string[]> IgnoreAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("ignore");
        return await QueryExecutor.ExecuteListAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The name of the argument in lowerCamelCase format.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> NameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("name");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The location of this arg declaration.
    /// </summary>
    public SourceMap SourceMap()
    {
        var queryBuilder = QueryBuilder.Select("sourceMap");
        return new SourceMap(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// The type of the argument.
    /// </summary>
    public TypeDef TypeDef()
    {
        var queryBuilder = QueryBuilder.Select("typeDef");
        return new TypeDef(queryBuilder, GraphQLClient);
    }
}

/// <summary>
/// The `FunctionArgID` scalar type represents an identifier for an object of type FunctionArg.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<FunctionArgId>))]
public class FunctionArgId : Scalar
{
}

/// <summary>
/// The behavior configured for function result caching.
/// </summary>
[JsonConverter(typeof(JsonStringEnumConverter<FunctionCachePolicy>))]
public enum FunctionCachePolicy
{
    /// <summary>Default</summary>
    Default,
    /// <summary>PerSession</summary>
    PerSession,
    /// <summary>Never</summary>
    Never
}

/// <summary>
/// An active function call.
/// </summary>
public class FunctionCall(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<FunctionCallId>
{
    /// <summary>
    /// A unique identifier for this FunctionCall.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<FunctionCallId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<FunctionCallId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The argument values the function is being invoked with.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<FunctionCallArgValue[]> InputArgsAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("inputArgs").Select("id");
        return (await QueryExecutor.ExecuteListAsync<FunctionCallArgValueId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new FunctionCallArgValue(QueryBuilder.Builder().Select("loadFunctionCallArgValueFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// The name of the function being called.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> NameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("name");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The value of the parent object of the function being called. If the function is top-level to the module, this is always an empty object.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Json> ParentAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("parent");
        return await QueryExecutor.ExecuteAsync<Json>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The name of the parent object of the function being called. If the function is top-level to the module, this is the name of the module.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> ParentNameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("parentName");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Return an error from the function.
    /// </summary>
    /// <param name = "error">
    /// The error to return.
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Void> ReturnErrorAsync(Error error, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("error", new IdValue<ErrorId>(error)));
        var queryBuilder = QueryBuilder.Select("returnError", arguments);
        return await QueryExecutor.ExecuteAsync<Void>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Set the return value of the function call to the provided value.
    /// </summary>
    /// <param name = "value">
    /// JSON serialization of the return value.
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Void> ReturnValueAsync(Json value, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("value", new StringValue(value.Value)));
        var queryBuilder = QueryBuilder.Select("returnValue", arguments);
        return await QueryExecutor.ExecuteAsync<Void>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// A value passed as a named argument to a function call.
/// </summary>
public class FunctionCallArgValue(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<FunctionCallArgValueId>
{
    /// <summary>
    /// A unique identifier for this FunctionCallArgValue.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<FunctionCallArgValueId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<FunctionCallArgValueId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The name of the argument.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> NameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("name");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The value of the argument represented as a JSON serialized string.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Json> ValueAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("value");
        return await QueryExecutor.ExecuteAsync<Json>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// The `FunctionCallArgValueID` scalar type represents an identifier for an object of type FunctionCallArgValue.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<FunctionCallArgValueId>))]
public class FunctionCallArgValueId : Scalar
{
}

/// <summary>
/// The `FunctionCallID` scalar type represents an identifier for an object of type FunctionCall.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<FunctionCallId>))]
public class FunctionCallId : Scalar
{
}

/// <summary>
/// The `FunctionID` scalar type represents an identifier for an object of type Function.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<FunctionId>))]
public class FunctionId : Scalar
{
}

/// <summary>
/// The result of running an SDK's codegen.
/// </summary>
public class GeneratedCode(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<GeneratedCodeId>
{
    /// <summary>
    /// The directory containing the generated code.
    /// </summary>
    public Directory Code()
    {
        var queryBuilder = QueryBuilder.Select("code");
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// A unique identifier for this GeneratedCode.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<GeneratedCodeId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<GeneratedCodeId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// List of paths to mark generated in version control (i.e. .gitattributes).
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string[]> VcsGeneratedPathsAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("vcsGeneratedPaths");
        return await QueryExecutor.ExecuteListAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// List of paths to ignore in version control (i.e. .gitignore).
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string[]> VcsIgnoredPathsAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("vcsIgnoredPaths");
        return await QueryExecutor.ExecuteListAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Set the list of paths to mark generated in version control.
    /// </summary>
    /// <param name = "paths">
    /// 
    /// </param>
    public GeneratedCode WithVcsgeneratedPaths(string[] paths)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("paths", new ListValue(paths.Select(v => new StringValue(v) as Value).ToList())));
        var queryBuilder = QueryBuilder.Select("withVCSGeneratedPaths", arguments);
        return new GeneratedCode(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Set the list of paths to ignore in version control.
    /// </summary>
    /// <param name = "paths">
    /// 
    /// </param>
    public GeneratedCode WithVcsignoredPaths(string[] paths)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("paths", new ListValue(paths.Select(v => new StringValue(v) as Value).ToList())));
        var queryBuilder = QueryBuilder.Select("withVCSIgnoredPaths", arguments);
        return new GeneratedCode(queryBuilder, GraphQLClient);
    }
}

/// <summary>
/// The `GeneratedCodeID` scalar type represents an identifier for an object of type GeneratedCode.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<GeneratedCodeId>))]
public class GeneratedCodeId : Scalar
{
}

/// <summary>
/// A git ref (tag, branch, or commit).
/// </summary>
public class GitRef(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<GitRefId>
{
    /// <summary>
    /// The resolved commit id at this ref.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> CommitAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("commit");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Find the best common ancestor between this ref and another ref.
    /// </summary>
    /// <param name = "other">
    /// The other ref to compare against.
    /// </param>
    public GitRef CommonAncestor(GitRef other)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("other", new IdValue<GitRefId>(other)));
        var queryBuilder = QueryBuilder.Select("commonAncestor", arguments);
        return new GitRef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// A unique identifier for this GitRef.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<GitRefId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<GitRefId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The resolved ref name at this ref.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> RefAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("ref");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The filesystem tree at this ref.
    /// </summary>
    /// <param name = "discardGitDir">
    /// Set to true to discard .git directory.
    /// </param>
    /// <param name = "depth">
    /// The depth of the tree to fetch.
    /// </param>
    public Directory Tree(bool? discardGitDir = false, int? depth = 1)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (discardGitDir is bool discardGitDir_)
        {
            arguments = arguments.Add(new Argument("discardGitDir", new BooleanValue(discardGitDir_)));
        }

        if (depth is int depth_)
        {
            arguments = arguments.Add(new Argument("depth", new IntValue(depth_)));
        }

        var queryBuilder = QueryBuilder.Select("tree", arguments);
        return new Directory(queryBuilder, GraphQLClient);
    }
}

/// <summary>
/// The `GitRefID` scalar type represents an identifier for an object of type GitRef.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<GitRefId>))]
public class GitRefId : Scalar
{
}

/// <summary>
/// A git repository.
/// </summary>
public class GitRepository(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<GitRepositoryId>
{
    /// <summary>
    /// Returns details of a branch.
    /// </summary>
    /// <param name = "name">
    /// Branch's name (e.g., "main").
    /// </param>
    public GitRef Branch(string name)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name)));
        var queryBuilder = QueryBuilder.Select("branch", arguments);
        return new GitRef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// branches that match any of the given glob patterns.
    /// </summary>
    /// <param name = "patterns">
    /// Glob patterns (e.g., "refs/tags/v*").
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string[]> BranchesAsync(string[]? patterns = null, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (patterns is string[] patterns_)
        {
            arguments = arguments.Add(new Argument("patterns", new ListValue(patterns_.Select(v => new StringValue(v) as Value).ToList())));
        }

        var queryBuilder = QueryBuilder.Select("branches", arguments);
        return await QueryExecutor.ExecuteListAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Returns details of a commit.
    /// </summary>
    /// <param name = "id">
    /// Identifier of the commit (e.g., "b6315d8f2810962c601af73f86831f6866ea798b").
    /// </param>
    public GitRef Commit(string id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id)));
        var queryBuilder = QueryBuilder.Select("commit", arguments);
        return new GitRef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Returns details for HEAD.
    /// </summary>
    public GitRef Head()
    {
        var queryBuilder = QueryBuilder.Select("head");
        return new GitRef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// A unique identifier for this GitRepository.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<GitRepositoryId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<GitRepositoryId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Returns details for the latest semver tag.
    /// </summary>
    public GitRef LatestVersion()
    {
        var queryBuilder = QueryBuilder.Select("latestVersion");
        return new GitRef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Returns details of a ref.
    /// </summary>
    /// <param name = "name">
    /// Ref's name (can be a commit identifier, a tag name, a branch name, or a fully-qualified ref).
    /// </param>
    public GitRef Ref(string name)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name)));
        var queryBuilder = QueryBuilder.Select("ref", arguments);
        return new GitRef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Returns details of a tag.
    /// </summary>
    /// <param name = "name">
    /// Tag's name (e.g., "v0.3.9").
    /// </param>
    public GitRef Tag(string name)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name)));
        var queryBuilder = QueryBuilder.Select("tag", arguments);
        return new GitRef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// tags that match any of the given glob patterns.
    /// </summary>
    /// <param name = "patterns">
    /// Glob patterns (e.g., "refs/tags/v*").
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string[]> TagsAsync(string[]? patterns = null, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (patterns is string[] patterns_)
        {
            arguments = arguments.Add(new Argument("patterns", new ListValue(patterns_.Select(v => new StringValue(v) as Value).ToList())));
        }

        var queryBuilder = QueryBuilder.Select("tags", arguments);
        return await QueryExecutor.ExecuteListAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Returns the changeset of uncommitted changes in the git repository.
    /// </summary>
    public Changeset Uncommitted()
    {
        var queryBuilder = QueryBuilder.Select("uncommitted");
        return new Changeset(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// The URL of the git repository.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> UrlAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("url");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// The `GitRepositoryID` scalar type represents an identifier for an object of type GitRepository.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<GitRepositoryId>))]
public class GitRepositoryId : Scalar
{
}

/// <summary>
/// Compression algorithm to use for image layers.
/// </summary>
[JsonConverter(typeof(JsonStringEnumConverter<ImageLayerCompression>))]
public enum ImageLayerCompression
{
    /// <summary>Gzip</summary>
    Gzip,
    /// <summary>Zstd</summary>
    Zstd,
    /// <summary>EStarGZ</summary>
    EStarGZ,
    /// <summary>Uncompressed</summary>
    Uncompressed,
    /// <summary>GZIP</summary>
    GZIP,
    /// <summary>ZSTD</summary>
    ZSTD,
    /// <summary>ESTARGZ</summary>
    ESTARGZ,
    /// <summary>UNCOMPRESSED</summary>
    UNCOMPRESSED
}

/// <summary>
/// Mediatypes to use in published or exported image metadata.
/// </summary>
[JsonConverter(typeof(JsonStringEnumConverter<ImageMediaTypes>))]
public enum ImageMediaTypes
{
    /// <summary>OCIMediaTypes</summary>
    OCIMediaTypes,
    /// <summary>DockerMediaTypes</summary>
    DockerMediaTypes,
    /// <summary>OCI</summary>
    OCI,
    /// <summary>DOCKER</summary>
    DOCKER
}

/// <summary>
/// A graphql input type, which is essentially just a group of named args.
/// This is currently only used to represent pre-existing usage of graphql input types
/// in the core API. It is not used by user modules and shouldn't ever be as user
/// module accept input objects via their id rather than graphql input types.
/// </summary>
public class InputTypeDef(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<InputTypeDefId>
{
    /// <summary>
    /// Static fields defined on this input object, if any.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<FieldTypeDef[]> FieldsAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("fields").Select("id");
        return (await QueryExecutor.ExecuteListAsync<FieldTypeDefId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new FieldTypeDef(QueryBuilder.Builder().Select("loadFieldTypeDefFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// A unique identifier for this InputTypeDef.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<InputTypeDefId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<InputTypeDefId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The name of the input object.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> NameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("name");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// The `InputTypeDefID` scalar type represents an identifier for an object of type InputTypeDef.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<InputTypeDefId>))]
public class InputTypeDefId : Scalar
{
}

/// <summary>
/// A definition of a custom interface defined in a Module.
/// </summary>
public class InterfaceTypeDef(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<InterfaceTypeDefId>
{
    /// <summary>
    /// The doc string for the interface, if any.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> DescriptionAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("description");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Functions defined on this interface, if any.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Function[]> FunctionsAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("functions").Select("id");
        return (await QueryExecutor.ExecuteListAsync<FunctionId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new Function(QueryBuilder.Builder().Select("loadFunctionFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// A unique identifier for this InterfaceTypeDef.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<InterfaceTypeDefId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<InterfaceTypeDefId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The name of the interface.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> NameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("name");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The location of this interface declaration.
    /// </summary>
    public SourceMap SourceMap()
    {
        var queryBuilder = QueryBuilder.Select("sourceMap");
        return new SourceMap(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// If this InterfaceTypeDef is associated with a Module, the name of the module. Unset otherwise.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> SourceModuleNameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("sourceModuleName");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// The `InterfaceTypeDefID` scalar type represents an identifier for an object of type InterfaceTypeDef.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<InterfaceTypeDefId>))]
public class InterfaceTypeDefId : Scalar
{
}

/// <summary>
/// An arbitrary JSON-encoded value.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<Json>))]
public class Json : Scalar
{
}

/// <summary>
/// JSONValue
/// </summary>
public class Jsonvalue(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<JsonvalueId>
{
    /// <summary>
    /// Decode an array from json
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Jsonvalue[]> AsArrayAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("asArray").Select("id");
        return (await QueryExecutor.ExecuteListAsync<JsonvalueId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new Jsonvalue(QueryBuilder.Builder().Select("loadJsonvalueFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// Decode a boolean from json
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<bool> AsBooleanAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("asBoolean");
        return await QueryExecutor.ExecuteAsync<bool>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Decode an integer from json
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<int> AsIntegerAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("asInteger");
        return await QueryExecutor.ExecuteAsync<int>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Decode a string from json
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> AsStringAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("asString");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Return the value encoded as json
    /// </summary>
    /// <param name = "pretty">
    /// Pretty-print
    /// </param>
    /// <param name = "indent">
    /// Optional line prefix
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Json> ContentsAsync(bool? pretty = false, string? indent = null, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (pretty is bool pretty_)
        {
            arguments = arguments.Add(new Argument("pretty", new BooleanValue(pretty_)));
        }

        if (indent is string indent_)
        {
            arguments = arguments.Add(new Argument("indent", new StringValue(indent_)));
        }

        var queryBuilder = QueryBuilder.Select("contents", arguments);
        return await QueryExecutor.ExecuteAsync<Json>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Lookup the field at the given path, and return its value.
    /// </summary>
    /// <param name = "path">
    /// Path of the field to lookup, encoded as an array of field names
    /// </param>
    public Jsonvalue Field(string[] path)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new ListValue(path.Select(v => new StringValue(v) as Value).ToList())));
        var queryBuilder = QueryBuilder.Select("field", arguments);
        return new Jsonvalue(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// List fields of the encoded object
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string[]> FieldsAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("fields");
        return await QueryExecutor.ExecuteListAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A unique identifier for this JSONValue.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<JsonvalueId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<JsonvalueId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Encode a boolean to json
    /// </summary>
    /// <param name = "value">
    /// New boolean value
    /// </param>
    public Jsonvalue NewBoolean(bool value)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("value", new BooleanValue(value)));
        var queryBuilder = QueryBuilder.Select("newBoolean", arguments);
        return new Jsonvalue(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Encode an integer to json
    /// </summary>
    /// <param name = "value">
    /// New integer value
    /// </param>
    public Jsonvalue NewInteger(int value)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("value", new IntValue(value)));
        var queryBuilder = QueryBuilder.Select("newInteger", arguments);
        return new Jsonvalue(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Encode a string to json
    /// </summary>
    /// <param name = "value">
    /// New string value
    /// </param>
    public Jsonvalue NewString(string value)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("value", new StringValue(value)));
        var queryBuilder = QueryBuilder.Select("newString", arguments);
        return new Jsonvalue(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Return a new json value, decoded from the given content
    /// </summary>
    /// <param name = "contents">
    /// New JSON-encoded contents
    /// </param>
    public Jsonvalue WithContents(Json contents)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("contents", new StringValue(contents.Value)));
        var queryBuilder = QueryBuilder.Select("withContents", arguments);
        return new Jsonvalue(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Set a new field at the given path
    /// </summary>
    /// <param name = "path">
    /// Path of the field to set, encoded as an array of field names
    /// </param>
    /// <param name = "value">
    /// The new value of the field
    /// </param>
    public Jsonvalue WithField(string[] path, Jsonvalue value)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new ListValue(path.Select(v => new StringValue(v) as Value).ToList()))).Add(new Argument("value", new IdValue<JsonvalueId>(value)));
        var queryBuilder = QueryBuilder.Select("withField", arguments);
        return new Jsonvalue(queryBuilder, GraphQLClient);
    }
}

/// <summary>
/// The `JSONValueID` scalar type represents an identifier for an object of type JSONValue.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<JsonvalueId>))]
public class JsonvalueId : Scalar
{
}

/// <summary>
/// LLM
/// </summary>
public class Llm(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<Llmid>
{
    /// <summary>
    /// create a branch in the LLM's history
    /// </summary>
    /// <param name = "number">
    /// 
    /// </param>
    public Llm Attempt(int number)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("number", new IntValue(number)));
        var queryBuilder = QueryBuilder.Select("attempt", arguments);
        return new Llm(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// returns the type of the current state
    /// </summary>
    /// <param name = "name">
    /// 
    /// </param>
    public Binding BindResult(string name)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name)));
        var queryBuilder = QueryBuilder.Select("bindResult", arguments);
        return new Binding(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// return the LLM's current environment
    /// </summary>
    public Env Env()
    {
        var queryBuilder = QueryBuilder.Select("env");
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Indicates whether there are any queued prompts or tool results to send to the model
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<bool> HasPromptAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("hasPrompt");
        return await QueryExecutor.ExecuteAsync<bool>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// return the llm message history
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string[]> HistoryAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("history");
        return await QueryExecutor.ExecuteListAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// return the raw llm message history as json
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Json> HistoryJsonAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("historyJSON");
        return await QueryExecutor.ExecuteAsync<Json>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A unique identifier for this LLM.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Llmid> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<Llmid>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// return the last llm reply from the history
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> LastReplyAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("lastReply");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Submit the queued prompt, evaluate any tool calls, queue their results, and keep going until the model ends its turn
    /// </summary>
    public Llm Loop()
    {
        var queryBuilder = QueryBuilder.Select("loop");
        return new Llm(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// return the model used by the llm
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> ModelAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("model");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// return the provider used by the llm
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> ProviderAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("provider");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Submit the queued prompt or tool call results, evaluate any tool calls, and queue their results
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Llmid> StepAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("step");
        return await QueryExecutor.ExecuteAsync<Llmid>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// synchronize LLM state
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Llmid> SyncAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("sync");
        return await QueryExecutor.ExecuteAsync<Llmid>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// returns the token usage of the current state
    /// </summary>
    public LlmtokenUsage TokenUsage()
    {
        var queryBuilder = QueryBuilder.Select("tokenUsage");
        return new LlmtokenUsage(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// print documentation for available tools
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> ToolsAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("tools");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Return a new LLM with the specified function no longer exposed as a tool
    /// </summary>
    /// <param name = "typeName">
    /// The type name whose function will be blocked
    /// </param>
    /// <param name = "function">
    /// The function to block
    /// 
    /// Will be converted to lowerCamelCase if necessary.
    /// </param>
    public Llm WithBlockedFunction(string typeName, string function)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("typeName", new StringValue(typeName))).Add(new Argument("function", new StringValue(function)));
        var queryBuilder = QueryBuilder.Select("withBlockedFunction", arguments);
        return new Llm(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// allow the LLM to interact with an environment via MCP
    /// </summary>
    /// <param name = "env">
    /// 
    /// </param>
    public Llm WithEnv(Env env)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("env", new IdValue<EnvId>(env)));
        var queryBuilder = QueryBuilder.Select("withEnv", arguments);
        return new Llm(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Add an external MCP server to the LLM
    /// </summary>
    /// <param name = "name">
    /// The name of the MCP server
    /// </param>
    /// <param name = "service">
    /// The MCP service to run and communicate with over stdio
    /// </param>
    public Llm WithMcpserver(string name, Service service)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("service", new IdValue<ServiceId>(service)));
        var queryBuilder = QueryBuilder.Select("withMCPServer", arguments);
        return new Llm(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// swap out the llm model
    /// </summary>
    /// <param name = "model">
    /// The model to use
    /// </param>
    public Llm WithModel(string model)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("model", new StringValue(model)));
        var queryBuilder = QueryBuilder.Select("withModel", arguments);
        return new Llm(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// append a prompt to the llm context
    /// </summary>
    /// <param name = "prompt">
    /// The prompt to send
    /// </param>
    public Llm WithPrompt(string prompt)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("prompt", new StringValue(prompt)));
        var queryBuilder = QueryBuilder.Select("withPrompt", arguments);
        return new Llm(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// append the contents of a file to the llm context
    /// </summary>
    /// <param name = "file">
    /// The file to read the prompt from
    /// </param>
    public Llm WithPromptFile(File file)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("file", new IdValue<FileId>(file)));
        var queryBuilder = QueryBuilder.Select("withPromptFile", arguments);
        return new Llm(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Use a static set of tools for method calls, e.g. for MCP clients that do not support dynamic tool registration
    /// </summary>
    public Llm WithStaticTools()
    {
        var queryBuilder = QueryBuilder.Select("withStaticTools");
        return new Llm(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Add a system prompt to the LLM's environment
    /// </summary>
    /// <param name = "prompt">
    /// The system prompt to send
    /// </param>
    public Llm WithSystemPrompt(string prompt)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("prompt", new StringValue(prompt)));
        var queryBuilder = QueryBuilder.Select("withSystemPrompt", arguments);
        return new Llm(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Disable the default system prompt
    /// </summary>
    public Llm WithoutDefaultSystemPrompt()
    {
        var queryBuilder = QueryBuilder.Select("withoutDefaultSystemPrompt");
        return new Llm(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Clear the message history, leaving only the system prompts
    /// </summary>
    public Llm WithoutMessageHistory()
    {
        var queryBuilder = QueryBuilder.Select("withoutMessageHistory");
        return new Llm(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Clear the system prompts, leaving only the default system prompt
    /// </summary>
    public Llm WithoutSystemPrompts()
    {
        var queryBuilder = QueryBuilder.Select("withoutSystemPrompts");
        return new Llm(queryBuilder, GraphQLClient);
    }
}

/// <summary>
/// The `LLMID` scalar type represents an identifier for an object of type LLM.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<Llmid>))]
public class Llmid : Scalar
{
}

/// <summary>
/// LLMTokenUsage
/// </summary>
public class LlmtokenUsage(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<LlmtokenUsageId>
{
    /// <summary>
    /// cachedTokenReads
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<int> CachedTokenReadsAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("cachedTokenReads");
        return await QueryExecutor.ExecuteAsync<int>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// cachedTokenWrites
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<int> CachedTokenWritesAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("cachedTokenWrites");
        return await QueryExecutor.ExecuteAsync<int>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A unique identifier for this LLMTokenUsage.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<LlmtokenUsageId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<LlmtokenUsageId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// inputTokens
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<int> InputTokensAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("inputTokens");
        return await QueryExecutor.ExecuteAsync<int>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// outputTokens
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<int> OutputTokensAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("outputTokens");
        return await QueryExecutor.ExecuteAsync<int>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// totalTokens
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<int> TotalTokensAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("totalTokens");
        return await QueryExecutor.ExecuteAsync<int>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// The `LLMTokenUsageID` scalar type represents an identifier for an object of type LLMTokenUsage.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<LlmtokenUsageId>))]
public class LlmtokenUsageId : Scalar
{
}

/// <summary>
/// A simple key value object that represents a label.
/// </summary>
public class Label(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<LabelId>
{
    /// <summary>
    /// A unique identifier for this Label.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<LabelId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<LabelId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The label name.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> NameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("name");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The label value.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> ValueAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("value");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// The `LabelID` scalar type represents an identifier for an object of type Label.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<LabelId>))]
public class LabelId : Scalar
{
}

/// <summary>
/// A definition of a list type in a Module.
/// </summary>
public class ListTypeDef(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<ListTypeDefId>
{
    /// <summary>
    /// The type of the elements in the list.
    /// </summary>
    public TypeDef ElementTypeDef()
    {
        var queryBuilder = QueryBuilder.Select("elementTypeDef");
        return new TypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// A unique identifier for this ListTypeDef.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<ListTypeDefId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<ListTypeDefId>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// The `ListTypeDefID` scalar type represents an identifier for an object of type ListTypeDef.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<ListTypeDefId>))]
public class ListTypeDefId : Scalar
{
}

/// <summary>
/// A Dagger module.
/// </summary>
public class Module(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<ModuleId>
{
    /// <summary>
    /// Return the check defined by the module with the given name. Must match to exactly one check.
    /// </summary>
    /// <param name = "name">
    /// The name of the check to retrieve
    /// </param>
    public Check Check(string name)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name)));
        var queryBuilder = QueryBuilder.Select("check", arguments);
        return new Check(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Return all checks defined by the module
    /// </summary>
    /// <param name = "include">
    /// Only include checks matching the specified patterns
    /// </param>
    public CheckGroup Checks(string[]? include = null)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (include is string[] include_)
        {
            arguments = arguments.Add(new Argument("include", new ListValue(include_.Select(v => new StringValue(v) as Value).ToList())));
        }

        var queryBuilder = QueryBuilder.Select("checks", arguments);
        return new CheckGroup(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// The dependencies of the module.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Module[]> DependenciesAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("dependencies").Select("id");
        return (await QueryExecutor.ExecuteListAsync<ModuleId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new Module(QueryBuilder.Builder().Select("loadModuleFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// The doc string of the module, if any
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> DescriptionAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("description");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Enumerations served by this module.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<TypeDef[]> EnumsAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("enums").Select("id");
        return (await QueryExecutor.ExecuteListAsync<TypeDefId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new TypeDef(QueryBuilder.Builder().Select("loadTypeDefFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// The generated files and directories made on top of the module source's context directory.
    /// </summary>
    public Directory GeneratedContextDirectory()
    {
        var queryBuilder = QueryBuilder.Select("generatedContextDirectory");
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// A unique identifier for this Module.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<ModuleId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<ModuleId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Interfaces served by this module.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<TypeDef[]> InterfacesAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("interfaces").Select("id");
        return (await QueryExecutor.ExecuteListAsync<TypeDefId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new TypeDef(QueryBuilder.Builder().Select("loadTypeDefFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// The introspection schema JSON file for this module.
    ///
    /// This file represents the schema visible to the module's source code, including all core types and those from the dependencies.
    ///
    /// Note: this is in the context of a module, so some core types may be hidden.
    /// </summary>
    public File IntrospectionSchemaJson()
    {
        var queryBuilder = QueryBuilder.Select("introspectionSchemaJSON");
        return new File(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// The name of the module
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> NameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("name");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Objects served by this module.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<TypeDef[]> ObjectsAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("objects").Select("id");
        return (await QueryExecutor.ExecuteListAsync<TypeDefId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new TypeDef(QueryBuilder.Builder().Select("loadTypeDefFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// The container that runs the module's entrypoint. It will fail to execute if the module doesn't compile.
    /// </summary>
    public Container Runtime()
    {
        var queryBuilder = QueryBuilder.Select("runtime");
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// The SDK config used by this module.
    /// </summary>
    public Sdkconfig Sdk()
    {
        var queryBuilder = QueryBuilder.Select("sdk");
        return new Sdkconfig(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Serve a module's API in the current session.
    ///
    /// Note: this can only be called once per session. In the future, it could return a stream or service to remove the side effect.
    /// </summary>
    /// <param name = "includeDependencies">
    /// Expose the dependencies of this module to the client
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Void> ServeAsync(bool? includeDependencies = null, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (includeDependencies is bool includeDependencies_)
        {
            arguments = arguments.Add(new Argument("includeDependencies", new BooleanValue(includeDependencies_)));
        }

        var queryBuilder = QueryBuilder.Select("serve", arguments);
        return await QueryExecutor.ExecuteAsync<Void>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The source for the module.
    /// </summary>
    public ModuleSource Source()
    {
        var queryBuilder = QueryBuilder.Select("source");
        return new ModuleSource(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Forces evaluation of the module, including any loading into the engine and associated validation.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<ModuleId> SyncAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("sync");
        return await QueryExecutor.ExecuteAsync<ModuleId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// User-defined default values, loaded from local .env files.
    /// </summary>
    public EnvFile UserDefaults()
    {
        var queryBuilder = QueryBuilder.Select("userDefaults");
        return new EnvFile(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Retrieves the module with the given description
    /// </summary>
    /// <param name = "description">
    /// The description to set
    /// </param>
    public Module WithDescription(string description)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("description", new StringValue(description)));
        var queryBuilder = QueryBuilder.Select("withDescription", arguments);
        return new Module(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// This module plus the given Enum type and associated values
    /// </summary>
    /// <param name = "enum_">
    /// 
    /// </param>
    public Module WithEnum(TypeDef enum_)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("enum", new IdValue<TypeDefId>(enum_)));
        var queryBuilder = QueryBuilder.Select("withEnum", arguments);
        return new Module(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// This module plus the given Interface type and associated functions
    /// </summary>
    /// <param name = "iface">
    /// 
    /// </param>
    public Module WithInterface(TypeDef iface)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("iface", new IdValue<TypeDefId>(iface)));
        var queryBuilder = QueryBuilder.Select("withInterface", arguments);
        return new Module(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// This module plus the given Object type and associated functions.
    /// </summary>
    /// <param name = "object_">
    /// 
    /// </param>
    public Module WithObject(TypeDef object_)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("object", new IdValue<TypeDefId>(object_)));
        var queryBuilder = QueryBuilder.Select("withObject", arguments);
        return new Module(queryBuilder, GraphQLClient);
    }
}

/// <summary>
/// The client generated for the module.
/// </summary>
public class ModuleConfigClient(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<ModuleConfigClientId>
{
    /// <summary>
    /// The directory the client is generated in.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> DirectoryAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("directory");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The generator to use
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> GeneratorAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("generator");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A unique identifier for this ModuleConfigClient.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<ModuleConfigClientId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<ModuleConfigClientId>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// The `ModuleConfigClientID` scalar type represents an identifier for an object of type ModuleConfigClient.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<ModuleConfigClientId>))]
public class ModuleConfigClientId : Scalar
{
}

/// <summary>
/// The `ModuleID` scalar type represents an identifier for an object of type Module.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<ModuleId>))]
public class ModuleId : Scalar
{
}

/// <summary>
/// The source needed to load and run a module, along with any metadata about the source such as versions/urls/etc.
/// </summary>
public class ModuleSource(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<ModuleSourceId>
{
    /// <summary>
    /// Load the source as a module. If this is a local source, the parent directory must have been provided during module source creation
    /// </summary>
    public Module AsModule()
    {
        var queryBuilder = QueryBuilder.Select("asModule");
        return new Module(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// A human readable ref string representation of this module source.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> AsStringAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("asString");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The blueprint referenced by the module source.
    /// </summary>
    public ModuleSource Blueprint()
    {
        var queryBuilder = QueryBuilder.Select("blueprint");
        return new ModuleSource(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// The ref to clone the root of the git repo from. Only valid for git sources.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> CloneRefAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("cloneRef");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The resolved commit of the git repo this source points to.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> CommitAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("commit");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The clients generated for the module.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<ModuleConfigClient[]> ConfigClientsAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("configClients").Select("id");
        return (await QueryExecutor.ExecuteListAsync<ModuleConfigClientId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new ModuleConfigClient(QueryBuilder.Builder().Select("loadModuleConfigClientFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// Whether an existing dagger.json for the module was found.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<bool> ConfigExistsAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("configExists");
        return await QueryExecutor.ExecuteAsync<bool>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The full directory loaded for the module source, including the source code as a subdirectory.
    /// </summary>
    public Directory ContextDirectory()
    {
        var queryBuilder = QueryBuilder.Select("contextDirectory");
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// The dependencies of the module source.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<ModuleSource[]> DependenciesAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("dependencies").Select("id");
        return (await QueryExecutor.ExecuteListAsync<ModuleSourceId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new ModuleSource(QueryBuilder.Builder().Select("loadModuleSourceFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// A content-hash of the module source. Module sources with the same digest will output the same generated context and convert into the same module instance.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> DigestAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("digest");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The directory containing the module configuration and source code (source code may be in a subdir).
    /// </summary>
    /// <param name = "path">
    /// A subpath from the source directory to select.
    /// </param>
    public Directory Directory(string path)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path)));
        var queryBuilder = QueryBuilder.Select("directory", arguments);
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// The engine version of the module.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> EngineVersionAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("engineVersion");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The generated files and directories made on top of the module source's context directory.
    /// </summary>
    public Directory GeneratedContextDirectory()
    {
        var queryBuilder = QueryBuilder.Select("generatedContextDirectory");
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// The URL to access the web view of the repository (e.g., GitHub, GitLab, Bitbucket).
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> HtmlRepoUrlAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("htmlRepoURL");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The URL to the source's git repo in a web browser. Only valid for git sources.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> HtmlUrlAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("htmlURL");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A unique identifier for this ModuleSource.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<ModuleSourceId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<ModuleSourceId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The introspection schema JSON file for this module source.
    ///
    /// This file represents the schema visible to the module's source code, including all core types and those from the dependencies.
    ///
    /// Note: this is in the context of a module, so some core types may be hidden.
    /// </summary>
    public File IntrospectionSchemaJson()
    {
        var queryBuilder = QueryBuilder.Select("introspectionSchemaJSON");
        return new File(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// The kind of module source (currently local, git or dir).
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<ModuleSourceKind> KindAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("kind");
        return await QueryExecutor.ExecuteAsync<ModuleSourceKind>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The full absolute path to the context directory on the caller's host filesystem that this module source is loaded from. Only valid for local module sources.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> LocalContextDirectoryPathAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("localContextDirectoryPath");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The name of the module, including any setting via the withName API.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> ModuleNameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("moduleName");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The original name of the module as read from the module's dagger.json (or set for the first time with the withName API).
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> ModuleOriginalNameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("moduleOriginalName");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The original subpath used when instantiating this module source, relative to the context directory.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> OriginalSubpathAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("originalSubpath");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The pinned version of this module source.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> PinAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("pin");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The import path corresponding to the root of the git repo this source points to. Only valid for git sources.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> RepoRootPathAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("repoRootPath");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The SDK configuration of the module.
    /// </summary>
    public Sdkconfig Sdk()
    {
        var queryBuilder = QueryBuilder.Select("sdk");
        return new Sdkconfig(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// The path, relative to the context directory, that contains the module's dagger.json.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> SourceRootSubpathAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("sourceRootSubpath");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The path to the directory containing the module's source code, relative to the context directory.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> SourceSubpathAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("sourceSubpath");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Forces evaluation of the module source, including any loading into the engine and associated validation.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<ModuleSourceId> SyncAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("sync");
        return await QueryExecutor.ExecuteAsync<ModuleSourceId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The toolchains referenced by the module source.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<ModuleSource[]> ToolchainsAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("toolchains").Select("id");
        return (await QueryExecutor.ExecuteListAsync<ModuleSourceId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new ModuleSource(QueryBuilder.Builder().Select("loadModuleSourceFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// User-defined defaults read from local .env files
    /// </summary>
    public EnvFile UserDefaults()
    {
        var queryBuilder = QueryBuilder.Select("userDefaults");
        return new EnvFile(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// The specified version of the git repo this source points to.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> VersionAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("version");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Set a blueprint for the module source.
    /// </summary>
    /// <param name = "blueprint">
    /// The blueprint module to set.
    /// </param>
    public ModuleSource WithBlueprint(ModuleSource blueprint)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("blueprint", new IdValue<ModuleSourceId>(blueprint)));
        var queryBuilder = QueryBuilder.Select("withBlueprint", arguments);
        return new ModuleSource(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Update the module source with a new client to generate.
    /// </summary>
    /// <param name = "generator">
    /// The generator to use
    /// </param>
    /// <param name = "outputDir">
    /// The output directory for the generated client.
    /// </param>
    public ModuleSource WithClient(string generator, string outputDir)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("generator", new StringValue(generator))).Add(new Argument("outputDir", new StringValue(outputDir)));
        var queryBuilder = QueryBuilder.Select("withClient", arguments);
        return new ModuleSource(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Append the provided dependencies to the module source's dependency list.
    /// </summary>
    /// <param name = "dependencies">
    /// The dependencies to append.
    /// </param>
    public ModuleSource WithDependencies(ModuleSource[] dependencies)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("dependencies", new ListValue(dependencies.Select(v => new IdValue<ModuleSourceId>(v) as Value).ToList())));
        var queryBuilder = QueryBuilder.Select("withDependencies", arguments);
        return new ModuleSource(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Upgrade the engine version of the module to the given value.
    /// </summary>
    /// <param name = "version">
    /// The engine version to upgrade to.
    /// </param>
    public ModuleSource WithEngineVersion(string version)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("version", new StringValue(version)));
        var queryBuilder = QueryBuilder.Select("withEngineVersion", arguments);
        return new ModuleSource(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Enable the experimental features for the module source.
    /// </summary>
    /// <param name = "features">
    /// The experimental features to enable.
    /// </param>
    public ModuleSource WithExperimentalFeatures(ModuleSourceExperimentalFeature[] features)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("features", new ListValue(features.Select(v => new StringValue(v.ToString()) as Value).ToList())));
        var queryBuilder = QueryBuilder.Select("withExperimentalFeatures", arguments);
        return new ModuleSource(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Update the module source with additional include patterns for files+directories from its context that are required for building it
    /// </summary>
    /// <param name = "patterns">
    /// The new additional include patterns.
    /// </param>
    public ModuleSource WithIncludes(string[] patterns)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("patterns", new ListValue(patterns.Select(v => new StringValue(v) as Value).ToList())));
        var queryBuilder = QueryBuilder.Select("withIncludes", arguments);
        return new ModuleSource(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Update the module source with a new name.
    /// </summary>
    /// <param name = "name">
    /// The name to set.
    /// </param>
    public ModuleSource WithName(string name)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name)));
        var queryBuilder = QueryBuilder.Select("withName", arguments);
        return new ModuleSource(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Update the module source with a new SDK.
    /// </summary>
    /// <param name = "source">
    /// The SDK source to set.
    /// </param>
    public ModuleSource WithSdk(string source)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("source", new StringValue(source)));
        var queryBuilder = QueryBuilder.Select("withSDK", arguments);
        return new ModuleSource(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Update the module source with a new source subpath.
    /// </summary>
    /// <param name = "path">
    /// The path to set as the source subpath. Must be relative to the module source's source root directory.
    /// </param>
    public ModuleSource WithSourceSubpath(string path)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path)));
        var queryBuilder = QueryBuilder.Select("withSourceSubpath", arguments);
        return new ModuleSource(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Add toolchains to the module source.
    /// </summary>
    /// <param name = "toolchains">
    /// The toolchain modules to add.
    /// </param>
    public ModuleSource WithToolchains(ModuleSource[] toolchains)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("toolchains", new ListValue(toolchains.Select(v => new IdValue<ModuleSourceId>(v) as Value).ToList())));
        var queryBuilder = QueryBuilder.Select("withToolchains", arguments);
        return new ModuleSource(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Update the blueprint module to the latest version.
    /// </summary>
    public ModuleSource WithUpdateBlueprint()
    {
        var queryBuilder = QueryBuilder.Select("withUpdateBlueprint");
        return new ModuleSource(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Update one or more module dependencies.
    /// </summary>
    /// <param name = "dependencies">
    /// The dependencies to update.
    /// </param>
    public ModuleSource WithUpdateDependencies(string[] dependencies)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("dependencies", new ListValue(dependencies.Select(v => new StringValue(v) as Value).ToList())));
        var queryBuilder = QueryBuilder.Select("withUpdateDependencies", arguments);
        return new ModuleSource(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Update one or more toolchains.
    /// </summary>
    /// <param name = "toolchains">
    /// The toolchains to update.
    /// </param>
    public ModuleSource WithUpdateToolchains(string[] toolchains)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("toolchains", new ListValue(toolchains.Select(v => new StringValue(v) as Value).ToList())));
        var queryBuilder = QueryBuilder.Select("withUpdateToolchains", arguments);
        return new ModuleSource(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Update one or more clients.
    /// </summary>
    /// <param name = "clients">
    /// The clients to update
    /// </param>
    public ModuleSource WithUpdatedClients(string[] clients)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("clients", new ListValue(clients.Select(v => new StringValue(v) as Value).ToList())));
        var queryBuilder = QueryBuilder.Select("withUpdatedClients", arguments);
        return new ModuleSource(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Remove the current blueprint from the module source.
    /// </summary>
    public ModuleSource WithoutBlueprint()
    {
        var queryBuilder = QueryBuilder.Select("withoutBlueprint");
        return new ModuleSource(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Remove a client from the module source.
    /// </summary>
    /// <param name = "path">
    /// The path of the client to remove.
    /// </param>
    public ModuleSource WithoutClient(string path)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("path", new StringValue(path)));
        var queryBuilder = QueryBuilder.Select("withoutClient", arguments);
        return new ModuleSource(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Remove the provided dependencies from the module source's dependency list.
    /// </summary>
    /// <param name = "dependencies">
    /// The dependencies to remove.
    /// </param>
    public ModuleSource WithoutDependencies(string[] dependencies)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("dependencies", new ListValue(dependencies.Select(v => new StringValue(v) as Value).ToList())));
        var queryBuilder = QueryBuilder.Select("withoutDependencies", arguments);
        return new ModuleSource(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Disable experimental features for the module source.
    /// </summary>
    /// <param name = "features">
    /// The experimental features to disable.
    /// </param>
    public ModuleSource WithoutExperimentalFeatures(ModuleSourceExperimentalFeature[] features)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("features", new ListValue(features.Select(v => new StringValue(v.ToString()) as Value).ToList())));
        var queryBuilder = QueryBuilder.Select("withoutExperimentalFeatures", arguments);
        return new ModuleSource(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Remove the provided toolchains from the module source.
    /// </summary>
    /// <param name = "toolchains">
    /// The toolchains to remove.
    /// </param>
    public ModuleSource WithoutToolchains(string[] toolchains)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("toolchains", new ListValue(toolchains.Select(v => new StringValue(v) as Value).ToList())));
        var queryBuilder = QueryBuilder.Select("withoutToolchains", arguments);
        return new ModuleSource(queryBuilder, GraphQLClient);
    }
}

/// <summary>
/// Experimental features of a module
/// </summary>
[JsonConverter(typeof(JsonStringEnumConverter<ModuleSourceExperimentalFeature>))]
public enum ModuleSourceExperimentalFeature
{
    /// <summary>
    /// Self calls
    /// </summary>
    SELF_CALLS
}

/// <summary>
/// The `ModuleSourceID` scalar type represents an identifier for an object of type ModuleSource.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<ModuleSourceId>))]
public class ModuleSourceId : Scalar
{
}

/// <summary>
/// The kind of module source.
/// </summary>
[JsonConverter(typeof(JsonStringEnumConverter<ModuleSourceKind>))]
public enum ModuleSourceKind
{
    /// <summary>LOCAL_SOURCE</summary>
    LOCAL_SOURCE,
    /// <summary>GIT_SOURCE</summary>
    GIT_SOURCE,
    /// <summary>DIR_SOURCE</summary>
    DIR_SOURCE,
    /// <summary>LOCAL</summary>
    LOCAL,
    /// <summary>GIT</summary>
    GIT,
    /// <summary>DIR</summary>
    DIR
}

/// <summary>
/// Transport layer network protocol associated to a port.
/// </summary>
[JsonConverter(typeof(JsonStringEnumConverter<NetworkProtocol>))]
public enum NetworkProtocol
{
    /// <summary>TCP</summary>
    TCP,
    /// <summary>UDP</summary>
    UDP
}

/// <summary>
/// A definition of a custom object defined in a Module.
/// </summary>
public class ObjectTypeDef(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<ObjectTypeDefId>
{
    /// <summary>
    /// The function used to construct new instances of this object, if any
    /// </summary>
    public Function Constructor()
    {
        var queryBuilder = QueryBuilder.Select("constructor");
        return new Function(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// The reason this enum member is deprecated, if any.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> DeprecatedAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("deprecated");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The doc string for the object, if any.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> DescriptionAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("description");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Static fields defined on this object, if any.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<FieldTypeDef[]> FieldsAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("fields").Select("id");
        return (await QueryExecutor.ExecuteListAsync<FieldTypeDefId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new FieldTypeDef(QueryBuilder.Builder().Select("loadFieldTypeDefFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// Functions defined on this object, if any.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Function[]> FunctionsAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("functions").Select("id");
        return (await QueryExecutor.ExecuteListAsync<FunctionId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new Function(QueryBuilder.Builder().Select("loadFunctionFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// A unique identifier for this ObjectTypeDef.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<ObjectTypeDefId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<ObjectTypeDefId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The name of the object.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> NameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("name");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The location of this object declaration.
    /// </summary>
    public SourceMap SourceMap()
    {
        var queryBuilder = QueryBuilder.Select("sourceMap");
        return new SourceMap(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// If this ObjectTypeDef is associated with a Module, the name of the module. Unset otherwise.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> SourceModuleNameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("sourceModuleName");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// The `ObjectTypeDefID` scalar type represents an identifier for an object of type ObjectTypeDef.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<ObjectTypeDefId>))]
public class ObjectTypeDefId : Scalar
{
}

/// <summary>
/// Key value object that represents a pipeline label.
/// </summary>
public struct PipelineLabel(string name, string value) : IInputObject
{
    /// <summary>
    /// Label name.
    /// </summary>
    public string Name { get; } = name;
    /// <summary>
    /// Label value.
    /// </summary>
    public string Value { get; } = value;

    /// <summary>
    /// Converts this input object to GraphQL key-value pairs.
    /// </summary>
    public List<KeyValuePair<string, Value>> ToKeyValuePairs()
    {
        var kvPairs = new List<KeyValuePair<string, Value>>();
        kvPairs.Add(new KeyValuePair<string, Value>("name", new StringValue(Name) as Value));
        kvPairs.Add(new KeyValuePair<string, Value>("value", new StringValue(Value) as Value));
        return kvPairs;
    }
}

/// <summary>
/// The platform config OS and architecture in a Container.
///
/// The format is [os]/[platform]/[version] (e.g., "darwin/arm64/v7", "windows/amd64", "linux/arm64").
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<Platform>))]
public class Platform : Scalar
{
}

/// <summary>
/// A port exposed by a container.
/// </summary>
public class Port(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<PortId>
{
    /// <summary>
    /// The port description.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> DescriptionAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("description");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Skip the health check when run as a service.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<bool> ExperimentalSkipHealthcheckAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("experimentalSkipHealthcheck");
        return await QueryExecutor.ExecuteAsync<bool>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A unique identifier for this Port.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<PortId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<PortId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The port number.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<int> Port_Async(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("port");
        return await QueryExecutor.ExecuteAsync<int>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The transport layer protocol.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<NetworkProtocol> ProtocolAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("protocol");
        return await QueryExecutor.ExecuteAsync<NetworkProtocol>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// Port forwarding rules for tunneling network traffic.
/// </summary>
public struct PortForward(int frontend, int backend, NetworkProtocol protocol) : IInputObject
{
    /// <summary>
    /// Port to expose to clients. If unspecified, a default will be chosen.
    /// </summary>
    public int Frontend { get; } = frontend;
    /// <summary>
    /// Destination port for traffic.
    /// </summary>
    public int Backend { get; } = backend;
    /// <summary>
    /// Transport layer protocol to use for traffic.
    /// </summary>
    public NetworkProtocol Protocol { get; } = protocol;

    /// <summary>
    /// Converts this input object to GraphQL key-value pairs.
    /// </summary>
    public List<KeyValuePair<string, Value>> ToKeyValuePairs()
    {
        var kvPairs = new List<KeyValuePair<string, Value>>();
        kvPairs.Add(new KeyValuePair<string, Value>("frontend", new IntValue(Frontend) as Value));
        kvPairs.Add(new KeyValuePair<string, Value>("backend", new IntValue(Backend) as Value));
        kvPairs.Add(new KeyValuePair<string, Value>("protocol", new StringValue(Protocol.ToString()) as Value));
        return kvPairs;
    }
}

/// <summary>
/// The `PortID` scalar type represents an identifier for an object of type Port.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<PortId>))]
public class PortId : Scalar
{
}

/// <summary>
/// The root of the DAG.
/// </summary>
public class Query(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient)
{
    /// <summary>
    /// initialize an address to load directories, containers, secrets or other object types.
    /// </summary>
    /// <param name = "value">
    /// 
    /// </param>
    public Address Address(string value)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("value", new StringValue(value)));
        var queryBuilder = QueryBuilder.Select("address", arguments);
        return new Address(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Constructs a cache volume for a given cache key.
    /// </summary>
    /// <param name = "key">
    /// A string identifier to target this cache volume (e.g., "modules-cache").
    /// </param>
    public CacheVolume CacheVolume(string key)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("key", new StringValue(key)));
        var queryBuilder = QueryBuilder.Select("cacheVolume", arguments);
        return new CacheVolume(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Dagger Cloud configuration and state
    /// </summary>
    public Cloud Cloud()
    {
        var queryBuilder = QueryBuilder.Select("cloud");
        return new Cloud(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Creates a scratch container, with no image or metadata.
    ///
    /// To pull an image, follow up with the "from" function.
    /// </summary>
    /// <param name = "platform">
    /// Platform to initialize the container with. Defaults to the native platform of the current engine
    /// </param>
    public Container Container(Platform? platform = null)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (platform is Platform platform_)
        {
            arguments = arguments.Add(new Argument("platform", new StringValue(platform_.Value)));
        }

        var queryBuilder = QueryBuilder.Select("container", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Returns the current environment
    ///
    /// When called from a function invoked via an LLM tool call, this will be the LLM's current environment, including any modifications made through calling tools. Env values returned by functions become the new environment for subsequent calls, and Changeset values returned by functions are applied to the environment's workspace.
    ///
    /// When called from a module function outside of an LLM, this returns an Env with the current module installed, and with the current module's source directory as its workspace.
    /// </summary>
    public Env CurrentEnv()
    {
        var queryBuilder = QueryBuilder.Select("currentEnv");
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// The FunctionCall context that the SDK caller is currently executing in.
    ///
    /// If the caller is not currently executing in a function, this will return an error.
    /// </summary>
    public FunctionCall CurrentFunctionCall()
    {
        var queryBuilder = QueryBuilder.Select("currentFunctionCall");
        return new FunctionCall(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// The module currently being served in the session, if any.
    /// </summary>
    public CurrentModule CurrentModule()
    {
        var queryBuilder = QueryBuilder.Select("currentModule");
        return new CurrentModule(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// The TypeDef representations of the objects currently being served in the session.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<TypeDef[]> CurrentTypeDefsAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("currentTypeDefs").Select("id");
        return (await QueryExecutor.ExecuteListAsync<TypeDefId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new TypeDef(QueryBuilder.Builder().Select("loadTypeDefFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// The default platform of the engine.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Platform> DefaultPlatformAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("defaultPlatform");
        return await QueryExecutor.ExecuteAsync<Platform>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Creates an empty directory.
    /// </summary>
    public Directory Directory()
    {
        var queryBuilder = QueryBuilder.Select("directory");
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Initializes a new environment
    /// </summary>
    /// <param name = "privileged">
    /// Give the environment the same privileges as the caller: core API including host access, current module, and dependencies
    /// </param>
    /// <param name = "writable">
    /// Allow new outputs to be declared and saved in the environment
    /// </param>
    public Env Env(bool? privileged = false, bool? writable = false)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (privileged is bool privileged_)
        {
            arguments = arguments.Add(new Argument("privileged", new BooleanValue(privileged_)));
        }

        if (writable is bool writable_)
        {
            arguments = arguments.Add(new Argument("writable", new BooleanValue(writable_)));
        }

        var queryBuilder = QueryBuilder.Select("env", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Initialize an environment file
    /// </summary>
    /// <param name = "expand">
    /// Replace "${VAR}" or "$VAR" with the value of other vars
    /// </param>
    public EnvFile EnvFile(bool? expand = null)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (expand is bool expand_)
        {
            arguments = arguments.Add(new Argument("expand", new BooleanValue(expand_)));
        }

        var queryBuilder = QueryBuilder.Select("envFile", arguments);
        return new EnvFile(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Create a new error.
    /// </summary>
    /// <param name = "message">
    /// A brief description of the error.
    /// </param>
    public Error Error(string message)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("message", new StringValue(message)));
        var queryBuilder = QueryBuilder.Select("error", arguments);
        return new Error(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Creates a file with the specified contents.
    /// </summary>
    /// <param name = "name">
    /// Name of the new file. Example: "foo.txt"
    /// </param>
    /// <param name = "contents">
    /// Contents of the new file. Example: "Hello world!"
    /// </param>
    /// <param name = "permissions">
    /// Permissions of the new file. Example: 0600
    /// </param>
    public File File(string name, string contents, int? permissions = 420)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("contents", new StringValue(contents)));
        if (permissions is int permissions_)
        {
            arguments = arguments.Add(new Argument("permissions", new IntValue(permissions_)));
        }

        var queryBuilder = QueryBuilder.Select("file", arguments);
        return new File(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Creates a function.
    /// </summary>
    /// <param name = "name">
    /// Name of the function, in its original format from the implementation language.
    /// </param>
    /// <param name = "returnType">
    /// Return type of the function.
    /// </param>
    public Function Function(string name, TypeDef returnType)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("returnType", new IdValue<TypeDefId>(returnType)));
        var queryBuilder = QueryBuilder.Select("function", arguments);
        return new Function(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Create a code generation result, given a directory containing the generated code.
    /// </summary>
    /// <param name = "code">
    /// 
    /// </param>
    public GeneratedCode GeneratedCode(Directory code)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("code", new IdValue<DirectoryId>(code)));
        var queryBuilder = QueryBuilder.Select("generatedCode", arguments);
        return new GeneratedCode(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Queries a Git repository.
    /// </summary>
    /// <param name = "url">
    /// URL of the git repository.
    /// 
    /// Can be formatted as `https://{host}/{owner}/{repo}`, `git@{host}:{owner}/{repo}`.
    /// 
    /// Suffix ".git" is optional.
    /// </param>
    /// <param name = "keepGitDir">
    /// DEPRECATED: Set to true to keep .git directory.
    /// </param>
    /// <param name = "sshKnownHosts">
    /// Set SSH known hosts
    /// </param>
    /// <param name = "sshAuthSocket">
    /// Set SSH auth socket
    /// </param>
    /// <param name = "httpAuthUsername">
    /// Username used to populate the password during basic HTTP Authorization
    /// </param>
    /// <param name = "httpAuthToken">
    /// Secret used to populate the password during basic HTTP Authorization
    /// </param>
    /// <param name = "httpAuthHeader">
    /// Secret used to populate the Authorization HTTP header
    /// </param>
    /// <param name = "experimentalServiceHost">
    /// A service which must be started before the repo is fetched.
    /// </param>
    public GitRepository Git(string url, bool? keepGitDir = true, string? sshKnownHosts = null, Socket? sshAuthSocket = null, string? httpAuthUsername = null, Secret? httpAuthToken = null, Secret? httpAuthHeader = null, Service? experimentalServiceHost = null)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("url", new StringValue(url)));
        if (keepGitDir is bool keepGitDir_)
        {
            arguments = arguments.Add(new Argument("keepGitDir", new BooleanValue(keepGitDir_)));
        }

        if (sshKnownHosts is string sshKnownHosts_)
        {
            arguments = arguments.Add(new Argument("sshKnownHosts", new StringValue(sshKnownHosts_)));
        }

        if (sshAuthSocket is Socket sshAuthSocket_)
        {
            arguments = arguments.Add(new Argument("sshAuthSocket", new IdValue<SocketId>(sshAuthSocket_)));
        }

        if (httpAuthUsername is string httpAuthUsername_)
        {
            arguments = arguments.Add(new Argument("httpAuthUsername", new StringValue(httpAuthUsername_)));
        }

        if (httpAuthToken is Secret httpAuthToken_)
        {
            arguments = arguments.Add(new Argument("httpAuthToken", new IdValue<SecretId>(httpAuthToken_)));
        }

        if (httpAuthHeader is Secret httpAuthHeader_)
        {
            arguments = arguments.Add(new Argument("httpAuthHeader", new IdValue<SecretId>(httpAuthHeader_)));
        }

        if (experimentalServiceHost is Service experimentalServiceHost_)
        {
            arguments = arguments.Add(new Argument("experimentalServiceHost", new IdValue<ServiceId>(experimentalServiceHost_)));
        }

        var queryBuilder = QueryBuilder.Select("git", arguments);
        return new GitRepository(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Returns a file containing an http remote url content.
    /// </summary>
    /// <param name = "url">
    /// HTTP url to get the content from (e.g., "https://docs.dagger.io").
    /// </param>
    /// <param name = "name">
    /// File name to use for the file. Defaults to the last part of the URL.
    /// </param>
    /// <param name = "permissions">
    /// Permissions to set on the file.
    /// </param>
    /// <param name = "authHeader">
    /// Secret used to populate the Authorization HTTP header
    /// </param>
    /// <param name = "experimentalServiceHost">
    /// A service which must be started before the URL is fetched.
    /// </param>
    public File Http(string url, string? name = null, int? permissions = null, Secret? authHeader = null, Service? experimentalServiceHost = null)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("url", new StringValue(url)));
        if (name is string name_)
        {
            arguments = arguments.Add(new Argument("name", new StringValue(name_)));
        }

        if (permissions is int permissions_)
        {
            arguments = arguments.Add(new Argument("permissions", new IntValue(permissions_)));
        }

        if (authHeader is Secret authHeader_)
        {
            arguments = arguments.Add(new Argument("authHeader", new IdValue<SecretId>(authHeader_)));
        }

        if (experimentalServiceHost is Service experimentalServiceHost_)
        {
            arguments = arguments.Add(new Argument("experimentalServiceHost", new IdValue<ServiceId>(experimentalServiceHost_)));
        }

        var queryBuilder = QueryBuilder.Select("http", arguments);
        return new File(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Initialize a JSON value
    /// </summary>
    public Jsonvalue Json()
    {
        var queryBuilder = QueryBuilder.Select("json");
        return new Jsonvalue(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Initialize a Large Language Model (LLM)
    /// </summary>
    /// <param name = "model">
    /// Model to use
    /// </param>
    /// <param name = "maxAPICalls">
    /// Cap the number of API calls for this LLM
    /// </param>
    public Llm Llm(string? model = null, int? maxAPICalls = null)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (model is string model_)
        {
            arguments = arguments.Add(new Argument("model", new StringValue(model_)));
        }

        if (maxAPICalls is int maxAPICalls_)
        {
            arguments = arguments.Add(new Argument("maxAPICalls", new IntValue(maxAPICalls_)));
        }

        var queryBuilder = QueryBuilder.Select("llm", arguments);
        return new Llm(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a Address from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public Address LoadAddressFromId(AddressId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadAddressFromID", arguments);
        return new Address(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a Binding from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public Binding LoadBindingFromId(BindingId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadBindingFromID", arguments);
        return new Binding(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a CacheVolume from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public CacheVolume LoadCacheVolumeFromId(CacheVolumeId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadCacheVolumeFromID", arguments);
        return new CacheVolume(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a Changeset from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public Changeset LoadChangesetFromId(ChangesetId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadChangesetFromID", arguments);
        return new Changeset(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a Check from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public Check LoadCheckFromId(CheckId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadCheckFromID", arguments);
        return new Check(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a CheckGroup from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public CheckGroup LoadCheckGroupFromId(CheckGroupId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadCheckGroupFromID", arguments);
        return new CheckGroup(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a Cloud from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public Cloud LoadCloudFromId(CloudId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadCloudFromID", arguments);
        return new Cloud(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a Container from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public Container LoadContainerFromId(ContainerId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadContainerFromID", arguments);
        return new Container(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a CurrentModule from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public CurrentModule LoadCurrentModuleFromId(CurrentModuleId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadCurrentModuleFromID", arguments);
        return new CurrentModule(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a Directory from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public Directory LoadDirectoryFromId(DirectoryId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadDirectoryFromID", arguments);
        return new Directory(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a EnumTypeDef from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public EnumTypeDef LoadEnumTypeDefFromId(EnumTypeDefId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadEnumTypeDefFromID", arguments);
        return new EnumTypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a EnumValueTypeDef from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public EnumValueTypeDef LoadEnumValueTypeDefFromId(EnumValueTypeDefId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadEnumValueTypeDefFromID", arguments);
        return new EnumValueTypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a EnvFile from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public EnvFile LoadEnvFileFromId(EnvFileId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadEnvFileFromID", arguments);
        return new EnvFile(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a Env from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public Env LoadEnvFromId(EnvId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadEnvFromID", arguments);
        return new Env(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a EnvVariable from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public EnvVariable LoadEnvVariableFromId(EnvVariableId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadEnvVariableFromID", arguments);
        return new EnvVariable(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a Error from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public Error LoadErrorFromId(ErrorId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadErrorFromID", arguments);
        return new Error(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a ErrorValue from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public ErrorValue LoadErrorValueFromId(ErrorValueId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadErrorValueFromID", arguments);
        return new ErrorValue(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a FieldTypeDef from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public FieldTypeDef LoadFieldTypeDefFromId(FieldTypeDefId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadFieldTypeDefFromID", arguments);
        return new FieldTypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a File from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public File LoadFileFromId(FileId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadFileFromID", arguments);
        return new File(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a FunctionArg from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public FunctionArg LoadFunctionArgFromId(FunctionArgId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadFunctionArgFromID", arguments);
        return new FunctionArg(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a FunctionCallArgValue from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public FunctionCallArgValue LoadFunctionCallArgValueFromId(FunctionCallArgValueId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadFunctionCallArgValueFromID", arguments);
        return new FunctionCallArgValue(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a FunctionCall from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public FunctionCall LoadFunctionCallFromId(FunctionCallId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadFunctionCallFromID", arguments);
        return new FunctionCall(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a Function from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public Function LoadFunctionFromId(FunctionId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadFunctionFromID", arguments);
        return new Function(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a GeneratedCode from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public GeneratedCode LoadGeneratedCodeFromId(GeneratedCodeId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadGeneratedCodeFromID", arguments);
        return new GeneratedCode(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a GitRef from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public GitRef LoadGitRefFromId(GitRefId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadGitRefFromID", arguments);
        return new GitRef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a GitRepository from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public GitRepository LoadGitRepositoryFromId(GitRepositoryId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadGitRepositoryFromID", arguments);
        return new GitRepository(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a InputTypeDef from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public InputTypeDef LoadInputTypeDefFromId(InputTypeDefId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadInputTypeDefFromID", arguments);
        return new InputTypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a InterfaceTypeDef from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public InterfaceTypeDef LoadInterfaceTypeDefFromId(InterfaceTypeDefId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadInterfaceTypeDefFromID", arguments);
        return new InterfaceTypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a JSONValue from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public Jsonvalue LoadJsonvalueFromId(JsonvalueId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadJSONValueFromID", arguments);
        return new Jsonvalue(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a LLM from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public Llm LoadLlmfromId(Llmid id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadLLMFromID", arguments);
        return new Llm(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a LLMTokenUsage from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public LlmtokenUsage LoadLlmtokenUsageFromId(LlmtokenUsageId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadLLMTokenUsageFromID", arguments);
        return new LlmtokenUsage(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a Label from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public Label LoadLabelFromId(LabelId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadLabelFromID", arguments);
        return new Label(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a ListTypeDef from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public ListTypeDef LoadListTypeDefFromId(ListTypeDefId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadListTypeDefFromID", arguments);
        return new ListTypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a ModuleConfigClient from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public ModuleConfigClient LoadModuleConfigClientFromId(ModuleConfigClientId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadModuleConfigClientFromID", arguments);
        return new ModuleConfigClient(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a Module from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public Module LoadModuleFromId(ModuleId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadModuleFromID", arguments);
        return new Module(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a ModuleSource from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public ModuleSource LoadModuleSourceFromId(ModuleSourceId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadModuleSourceFromID", arguments);
        return new ModuleSource(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a ObjectTypeDef from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public ObjectTypeDef LoadObjectTypeDefFromId(ObjectTypeDefId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadObjectTypeDefFromID", arguments);
        return new ObjectTypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a Port from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public Port LoadPortFromId(PortId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadPortFromID", arguments);
        return new Port(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a SDKConfig from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public Sdkconfig LoadSdkconfigFromId(SdkconfigId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadSDKConfigFromID", arguments);
        return new Sdkconfig(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a ScalarTypeDef from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public ScalarTypeDef LoadScalarTypeDefFromId(ScalarTypeDefId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadScalarTypeDefFromID", arguments);
        return new ScalarTypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a SearchResult from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public SearchResult LoadSearchResultFromId(SearchResultId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadSearchResultFromID", arguments);
        return new SearchResult(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a SearchSubmatch from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public SearchSubmatch LoadSearchSubmatchFromId(SearchSubmatchId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadSearchSubmatchFromID", arguments);
        return new SearchSubmatch(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a Secret from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public Secret LoadSecretFromId(SecretId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadSecretFromID", arguments);
        return new Secret(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a Service from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public Service LoadServiceFromId(ServiceId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadServiceFromID", arguments);
        return new Service(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a Socket from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public Socket LoadSocketFromId(SocketId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadSocketFromID", arguments);
        return new Socket(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a SourceMap from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public SourceMap LoadSourceMapFromId(SourceMapId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadSourceMapFromID", arguments);
        return new SourceMap(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a Terminal from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public Terminal LoadTerminalFromId(TerminalId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadTerminalFromID", arguments);
        return new Terminal(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Load a TypeDef from its ID.
    /// </summary>
    /// <param name = "id">
    /// 
    /// </param>
    public TypeDef LoadTypeDefFromId(TypeDefId id)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(id.Value)));
        var queryBuilder = QueryBuilder.Select("loadTypeDefFromID", arguments);
        return new TypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Create a new module.
    /// </summary>
    public Module Module()
    {
        var queryBuilder = QueryBuilder.Select("module");
        return new Module(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Create a new module source instance from a source ref string
    /// </summary>
    /// <param name = "refString">
    /// The string ref representation of the module source
    /// </param>
    /// <param name = "refPin">
    /// The pinned version of the module source
    /// </param>
    /// <param name = "disableFindUp">
    /// If true, do not attempt to find dagger.json in a parent directory of the provided path. Only relevant for local module sources.
    /// </param>
    /// <param name = "allowNotExists">
    /// If true, do not error out if the provided ref string is a local path and does not exist yet. Useful when initializing new modules in directories that don't exist yet.
    /// </param>
    /// <param name = "requireKind">
    /// If set, error out if the ref string is not of the provided requireKind.
    /// </param>
    public ModuleSource ModuleSource(string refString, string? refPin = null, bool? disableFindUp = false, bool? allowNotExists = false, ModuleSourceKind? requireKind = null)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("refString", new StringValue(refString)));
        if (refPin is string refPin_)
        {
            arguments = arguments.Add(new Argument("refPin", new StringValue(refPin_)));
        }

        if (disableFindUp is bool disableFindUp_)
        {
            arguments = arguments.Add(new Argument("disableFindUp", new BooleanValue(disableFindUp_)));
        }

        if (allowNotExists is bool allowNotExists_)
        {
            arguments = arguments.Add(new Argument("allowNotExists", new BooleanValue(allowNotExists_)));
        }

        if (requireKind is ModuleSourceKind requireKind_)
        {
            arguments = arguments.Add(new Argument("requireKind", new StringValue(requireKind_.ToString())));
        }

        var queryBuilder = QueryBuilder.Select("moduleSource", arguments);
        return new ModuleSource(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Creates a new secret.
    /// </summary>
    /// <param name = "uri">
    /// The URI of the secret store
    /// </param>
    /// <param name = "cacheKey">
    /// If set, the given string will be used as the cache key for this secret. This means that any secrets with the same cache key will be considered equivalent in terms of cache lookups, even if they have different URIs or plaintext values.
    /// 
    /// For example, two secrets with the same cache key provided as secret env vars to other wise equivalent containers will result in the container withExecs hitting the cache for each other.
    /// 
    /// If not set, the cache key for the secret will be derived from its plaintext value as looked up when the secret is constructed.
    /// </param>
    public Secret Secret(string uri, string? cacheKey = null)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("uri", new StringValue(uri)));
        if (cacheKey is string cacheKey_)
        {
            arguments = arguments.Add(new Argument("cacheKey", new StringValue(cacheKey_)));
        }

        var queryBuilder = QueryBuilder.Select("secret", arguments);
        return new Secret(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Sets a secret given a user defined name to its plaintext and returns the secret.
    ///
    /// The plaintext value is limited to a size of 128000 bytes.
    /// </summary>
    /// <param name = "name">
    /// The user defined name for this secret
    /// </param>
    /// <param name = "plaintext">
    /// The plaintext of the secret
    /// </param>
    public Secret SetSecret(string name, string plaintext)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("plaintext", new StringValue(plaintext)));
        var queryBuilder = QueryBuilder.Select("setSecret", arguments);
        return new Secret(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Creates source map metadata.
    /// </summary>
    /// <param name = "filename">
    /// The filename from the module source.
    /// </param>
    /// <param name = "line">
    /// The line number within the filename.
    /// </param>
    /// <param name = "column">
    /// The column number within the line.
    /// </param>
    public SourceMap SourceMap(string filename, int line, int column)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("filename", new StringValue(filename))).Add(new Argument("line", new IntValue(line))).Add(new Argument("column", new IntValue(column)));
        var queryBuilder = QueryBuilder.Select("sourceMap", arguments);
        return new SourceMap(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Create a new TypeDef.
    /// </summary>
    public TypeDef TypeDef()
    {
        var queryBuilder = QueryBuilder.Select("typeDef");
        return new TypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Get the current Dagger Engine version.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> VersionAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("version");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// Expected return type of an execution
/// </summary>
[JsonConverter(typeof(JsonStringEnumConverter<ReturnType>))]
public enum ReturnType
{
    /// <summary>
    /// A successful execution (exit code 0)
    /// </summary>
    SUCCESS,
    /// <summary>
    /// A failed execution (exit codes 1-127)
    /// </summary>
    FAILURE,
    /// <summary>
    /// Any execution (exit codes 0-127)
    /// </summary>
    ANY
}

/// <summary>
/// The SDK config of the module.
/// </summary>
public class Sdkconfig(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<SdkconfigId>
{
    /// <summary>
    /// Whether to start the SDK runtime in debug mode with an interactive terminal.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<bool> DebugAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("debug");
        return await QueryExecutor.ExecuteAsync<bool>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A unique identifier for this SDKConfig.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<SdkconfigId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<SdkconfigId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Source of the SDK. Either a name of a builtin SDK or a module source ref string pointing to the SDK's implementation.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> SourceAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("source");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// The `SDKConfigID` scalar type represents an identifier for an object of type SDKConfig.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<SdkconfigId>))]
public class SdkconfigId : Scalar
{
}

/// <summary>
/// A definition of a custom scalar defined in a Module.
/// </summary>
public class ScalarTypeDef(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<ScalarTypeDefId>
{
    /// <summary>
    /// A doc string for the scalar, if any.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> DescriptionAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("description");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A unique identifier for this ScalarTypeDef.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<ScalarTypeDefId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<ScalarTypeDefId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The name of the scalar.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> NameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("name");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// If this ScalarTypeDef is associated with a Module, the name of the module. Unset otherwise.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> SourceModuleNameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("sourceModuleName");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// The `ScalarTypeDefID` scalar type represents an identifier for an object of type ScalarTypeDef.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<ScalarTypeDefId>))]
public class ScalarTypeDefId : Scalar
{
}

/// <summary>
/// SearchResult
/// </summary>
public class SearchResult(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<SearchResultId>
{
    /// <summary>
    /// The byte offset of this line within the file.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<int> AbsoluteOffsetAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("absoluteOffset");
        return await QueryExecutor.ExecuteAsync<int>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The path to the file that matched.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> FilePathAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("filePath");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A unique identifier for this SearchResult.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<SearchResultId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<SearchResultId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The first line that matched.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<int> LineNumberAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("lineNumber");
        return await QueryExecutor.ExecuteAsync<int>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The line content that matched.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> MatchedLinesAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("matchedLines");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Sub-match positions and content within the matched lines.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<SearchSubmatch[]> SubmatchesAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("submatches").Select("id");
        return (await QueryExecutor.ExecuteListAsync<SearchSubmatchId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new SearchSubmatch(QueryBuilder.Builder().Select("loadSearchSubmatchFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }
}

/// <summary>
/// The `SearchResultID` scalar type represents an identifier for an object of type SearchResult.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<SearchResultId>))]
public class SearchResultId : Scalar
{
}

/// <summary>
/// SearchSubmatch
/// </summary>
public class SearchSubmatch(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<SearchSubmatchId>
{
    /// <summary>
    /// The match's end offset within the matched lines.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<int> EndAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("end");
        return await QueryExecutor.ExecuteAsync<int>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A unique identifier for this SearchSubmatch.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<SearchSubmatchId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<SearchSubmatchId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The match's start offset within the matched lines.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<int> StartAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("start");
        return await QueryExecutor.ExecuteAsync<int>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The matched text.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> TextAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("text");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// The `SearchSubmatchID` scalar type represents an identifier for an object of type SearchSubmatch.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<SearchSubmatchId>))]
public class SearchSubmatchId : Scalar
{
}

/// <summary>
/// A reference to a secret value, which can be handled more safely than the value itself.
/// </summary>
public class Secret(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<SecretId>
{
    /// <summary>
    /// A unique identifier for this Secret.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<SecretId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<SecretId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The name of this secret.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> NameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("name");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The value of this secret.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> PlaintextAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("plaintext");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The URI of this secret.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> UriAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("uri");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// The `SecretID` scalar type represents an identifier for an object of type Secret.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<SecretId>))]
public class SecretId : Scalar
{
}

/// <summary>
/// A content-addressed service providing TCP connectivity.
/// </summary>
public class Service(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<ServiceId>
{
    /// <summary>
    /// Retrieves an endpoint that clients can use to reach this container.
    ///
    /// If no port is specified, the first exposed port is used. If none exist an error is returned.
    ///
    /// If a scheme is specified, a URL is returned. Otherwise, a host:port pair is returned.
    /// </summary>
    /// <param name = "port">
    /// The exposed port number for the endpoint
    /// </param>
    /// <param name = "scheme">
    /// Return a URL with the given scheme, eg. http for http://
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> EndpointAsync(int? port = null, string? scheme = null, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (port is int port_)
        {
            arguments = arguments.Add(new Argument("port", new IntValue(port_)));
        }

        if (scheme is string scheme_)
        {
            arguments = arguments.Add(new Argument("scheme", new StringValue(scheme_)));
        }

        var queryBuilder = QueryBuilder.Select("endpoint", arguments);
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Retrieves a hostname which can be used by clients to reach this container.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> HostnameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("hostname");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A unique identifier for this Service.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<ServiceId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<ServiceId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Retrieves the list of ports provided by the service.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Port[]> PortsAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("ports").Select("id");
        return (await QueryExecutor.ExecuteListAsync<PortId>(GraphQLClient, queryBuilder, cancellationToken)).Select(id => new Port(QueryBuilder.Builder().Select("loadPortFromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))), GraphQLClient)).ToArray();
    }

    /// <summary>
    /// Start the service and wait for its health checks to succeed.
    ///
    /// Services bound to a Container do not need to be manually started.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<ServiceId> StartAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("start");
        return await QueryExecutor.ExecuteAsync<ServiceId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Stop the service.
    /// </summary>
    /// <param name = "kill">
    /// Immediately kill the service without waiting for a graceful exit
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<ServiceId> StopAsync(bool? kill = false, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (kill is bool kill_)
        {
            arguments = arguments.Add(new Argument("kill", new BooleanValue(kill_)));
        }

        var queryBuilder = QueryBuilder.Select("stop", arguments);
        return await QueryExecutor.ExecuteAsync<ServiceId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Forces evaluation of the pipeline in the engine.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<ServiceId> SyncAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("sync");
        return await QueryExecutor.ExecuteAsync<ServiceId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// terminal
    /// </summary>
    /// <param name = "cmd">
    /// 
    /// </param>
    public Service Terminal(string[]? cmd = null)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (cmd is string[] cmd_)
        {
            arguments = arguments.Add(new Argument("cmd", new ListValue(cmd_.Select(v => new StringValue(v) as Value).ToList())));
        }

        var queryBuilder = QueryBuilder.Select("terminal", arguments);
        return new Service(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Creates a tunnel that forwards traffic from the caller's network to this service.
    /// </summary>
    /// <param name = "ports">
    /// List of frontend/backend port mappings to forward.
    /// 
    /// Frontend is the port accepting traffic on the host, backend is the service port.
    /// </param>
    /// <param name = "random">
    /// Bind each tunnel port to a random port on the host.
    /// </param>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<Void> UpAsync(PortForward[]? ports = null, bool? random = false, CancellationToken cancellationToken = default)
    {
        var arguments = ImmutableList<Argument>.Empty;
        if (ports is PortForward[] ports_)
        {
            arguments = arguments.Add(new Argument("ports", new ListValue(ports_.Select(v => new ObjectValue(v.ToKeyValuePairs()) as Value).ToList())));
        }

        if (random is bool random_)
        {
            arguments = arguments.Add(new Argument("random", new BooleanValue(random_)));
        }

        var queryBuilder = QueryBuilder.Select("up", arguments);
        return await QueryExecutor.ExecuteAsync<Void>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Configures a hostname which can be used by clients within the session to reach this container.
    /// </summary>
    /// <param name = "hostname">
    /// The hostname to use.
    /// </param>
    public Service WithHostname(string hostname)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("hostname", new StringValue(hostname)));
        var queryBuilder = QueryBuilder.Select("withHostname", arguments);
        return new Service(queryBuilder, GraphQLClient);
    }
}

/// <summary>
/// The `ServiceID` scalar type represents an identifier for an object of type Service.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<ServiceId>))]
public class ServiceId : Scalar
{
}

/// <summary>
/// A Unix or TCP/IP socket that can be mounted into a container.
/// </summary>
public class Socket(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<SocketId>
{
    /// <summary>
    /// A unique identifier for this Socket.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<SocketId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<SocketId>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// The `SocketID` scalar type represents an identifier for an object of type Socket.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<SocketId>))]
public class SocketId : Scalar
{
}

/// <summary>
/// Source location information.
/// </summary>
public class SourceMap(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<SourceMapId>
{
    /// <summary>
    /// The column number within the line.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<int> ColumnAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("column");
        return await QueryExecutor.ExecuteAsync<int>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The filename from the module source.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> FilenameAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("filename");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// A unique identifier for this SourceMap.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<SourceMapId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<SourceMapId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The line number within the filename.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<int> LineAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("line");
        return await QueryExecutor.ExecuteAsync<int>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The module dependency this was declared in.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> ModuleAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("module");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The URL to the file, if any. This can be used to link to the source map in the browser.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<string> UrlAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("url");
        return await QueryExecutor.ExecuteAsync<string>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// The `SourceMapID` scalar type represents an identifier for an object of type SourceMap.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<SourceMapId>))]
public class SourceMapId : Scalar
{
}

/// <summary>
/// An interactive terminal that clients can connect to.
/// </summary>
public class Terminal(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<TerminalId>
{
    /// <summary>
    /// A unique identifier for this Terminal.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<TerminalId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<TerminalId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Forces evaluation of the pipeline in the engine.
    ///
    /// It doesn't run the default command if no exec has been set.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<TerminalId> SyncAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("sync");
        return await QueryExecutor.ExecuteAsync<TerminalId>(GraphQLClient, queryBuilder, cancellationToken);
    }
}

/// <summary>
/// The `TerminalID` scalar type represents an identifier for an object of type Terminal.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<TerminalId>))]
public class TerminalId : Scalar
{
}

/// <summary>
/// A definition of a parameter or return type in a Module.
/// </summary>
public class TypeDef(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient), IId<TypeDefId>
{
    /// <summary>
    /// If kind is ENUM, the enum-specific type definition. If kind is not ENUM, this will be null.
    /// </summary>
    public EnumTypeDef AsEnum()
    {
        var queryBuilder = QueryBuilder.Select("asEnum");
        return new EnumTypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// If kind is INPUT, the input-specific type definition. If kind is not INPUT, this will be null.
    /// </summary>
    public InputTypeDef AsInput()
    {
        var queryBuilder = QueryBuilder.Select("asInput");
        return new InputTypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// If kind is INTERFACE, the interface-specific type definition. If kind is not INTERFACE, this will be null.
    /// </summary>
    public InterfaceTypeDef AsInterface()
    {
        var queryBuilder = QueryBuilder.Select("asInterface");
        return new InterfaceTypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// If kind is LIST, the list-specific type definition. If kind is not LIST, this will be null.
    /// </summary>
    public ListTypeDef AsList()
    {
        var queryBuilder = QueryBuilder.Select("asList");
        return new ListTypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// If kind is OBJECT, the object-specific type definition. If kind is not OBJECT, this will be null.
    /// </summary>
    public ObjectTypeDef AsObject()
    {
        var queryBuilder = QueryBuilder.Select("asObject");
        return new ObjectTypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// If kind is SCALAR, the scalar-specific type definition. If kind is not SCALAR, this will be null.
    /// </summary>
    public ScalarTypeDef AsScalar()
    {
        var queryBuilder = QueryBuilder.Select("asScalar");
        return new ScalarTypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// A unique identifier for this TypeDef.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<TypeDefId> IdAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("id");
        return await QueryExecutor.ExecuteAsync<TypeDefId>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// The kind of type this is (e.g. primitive, list, object).
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<TypeDefKind> KindAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("kind");
        return await QueryExecutor.ExecuteAsync<TypeDefKind>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Whether this type can be set to null. Defaults to false.
    /// </summary>
    /// <param name = "cancellationToken">
    /// A cancellation token that can be used to cancel the operation.
    /// </param>
    public async Task<bool> OptionalAsync(CancellationToken cancellationToken = default)
    {
        var queryBuilder = QueryBuilder.Select("optional");
        return await QueryExecutor.ExecuteAsync<bool>(GraphQLClient, queryBuilder, cancellationToken);
    }

    /// <summary>
    /// Adds a function for constructing a new instance of an Object TypeDef, failing if the type is not an object.
    /// </summary>
    /// <param name = "function">
    /// 
    /// </param>
    public TypeDef WithConstructor(Function function)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("function", new IdValue<FunctionId>(function)));
        var queryBuilder = QueryBuilder.Select("withConstructor", arguments);
        return new TypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Returns a TypeDef of kind Enum with the provided name.
    ///
    /// Note that an enum's values may be omitted if the intent is only to refer to an enum. This is how functions are able to return their own, or any other circular reference.
    /// </summary>
    /// <param name = "name">
    /// The name of the enum
    /// </param>
    /// <param name = "description">
    /// A doc string for the enum, if any
    /// </param>
    /// <param name = "sourceMap">
    /// The source map for the enum definition.
    /// </param>
    public TypeDef WithEnum(string name, string? description = null, SourceMap? sourceMap = null)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name)));
        if (description is string description_)
        {
            arguments = arguments.Add(new Argument("description", new StringValue(description_)));
        }

        if (sourceMap is SourceMap sourceMap_)
        {
            arguments = arguments.Add(new Argument("sourceMap", new IdValue<SourceMapId>(sourceMap_)));
        }

        var queryBuilder = QueryBuilder.Select("withEnum", arguments);
        return new TypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Adds a static value for an Enum TypeDef, failing if the type is not an enum.
    /// </summary>
    /// <param name = "name">
    /// The name of the member in the enum
    /// </param>
    /// <param name = "value">
    /// The value of the member in the enum
    /// </param>
    /// <param name = "description">
    /// A doc string for the member, if any
    /// </param>
    /// <param name = "sourceMap">
    /// The source map for the enum member definition.
    /// </param>
    /// <param name = "deprecated">
    /// If deprecated, the reason or migration path.
    /// </param>
    public TypeDef WithEnumMember(string name, string? value = null, string? description = null, SourceMap? sourceMap = null, string? deprecated = null)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name)));
        if (value is string value_)
        {
            arguments = arguments.Add(new Argument("value", new StringValue(value_)));
        }

        if (description is string description_)
        {
            arguments = arguments.Add(new Argument("description", new StringValue(description_)));
        }

        if (sourceMap is SourceMap sourceMap_)
        {
            arguments = arguments.Add(new Argument("sourceMap", new IdValue<SourceMapId>(sourceMap_)));
        }

        if (deprecated is string deprecated_)
        {
            arguments = arguments.Add(new Argument("deprecated", new StringValue(deprecated_)));
        }

        var queryBuilder = QueryBuilder.Select("withEnumMember", arguments);
        return new TypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Adds a static value for an Enum TypeDef, failing if the type is not an enum.
    /// </summary>
    /// <param name = "value">
    /// The name of the value in the enum
    /// </param>
    /// <param name = "description">
    /// A doc string for the value, if any
    /// </param>
    /// <param name = "sourceMap">
    /// The source map for the enum value definition.
    /// </param>
    /// <param name = "deprecated">
    /// If deprecated, the reason or migration path.
    /// </param>
    [Obsolete("Use `withEnumMember` instead")]
    public TypeDef WithEnumValue(string value, string? description = null, SourceMap? sourceMap = null, string? deprecated = null)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("value", new StringValue(value)));
        if (description is string description_)
        {
            arguments = arguments.Add(new Argument("description", new StringValue(description_)));
        }

        if (sourceMap is SourceMap sourceMap_)
        {
            arguments = arguments.Add(new Argument("sourceMap", new IdValue<SourceMapId>(sourceMap_)));
        }

        if (deprecated is string deprecated_)
        {
            arguments = arguments.Add(new Argument("deprecated", new StringValue(deprecated_)));
        }

        var queryBuilder = QueryBuilder.Select("withEnumValue", arguments);
        return new TypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Adds a static field for an Object TypeDef, failing if the type is not an object.
    /// </summary>
    /// <param name = "name">
    /// The name of the field in the object
    /// </param>
    /// <param name = "typeDef">
    /// The type of the field
    /// </param>
    /// <param name = "description">
    /// A doc string for the field, if any
    /// </param>
    /// <param name = "sourceMap">
    /// The source map for the field definition.
    /// </param>
    /// <param name = "deprecated">
    /// If deprecated, the reason or migration path.
    /// </param>
    public TypeDef WithField(string name, TypeDef typeDef, string? description = null, SourceMap? sourceMap = null, string? deprecated = null)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name))).Add(new Argument("typeDef", new IdValue<TypeDefId>(typeDef)));
        if (description is string description_)
        {
            arguments = arguments.Add(new Argument("description", new StringValue(description_)));
        }

        if (sourceMap is SourceMap sourceMap_)
        {
            arguments = arguments.Add(new Argument("sourceMap", new IdValue<SourceMapId>(sourceMap_)));
        }

        if (deprecated is string deprecated_)
        {
            arguments = arguments.Add(new Argument("deprecated", new StringValue(deprecated_)));
        }

        var queryBuilder = QueryBuilder.Select("withField", arguments);
        return new TypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Adds a function for an Object or Interface TypeDef, failing if the type is not one of those kinds.
    /// </summary>
    /// <param name = "function">
    /// 
    /// </param>
    public TypeDef WithFunction(Function function)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("function", new IdValue<FunctionId>(function)));
        var queryBuilder = QueryBuilder.Select("withFunction", arguments);
        return new TypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Returns a TypeDef of kind Interface with the provided name.
    /// </summary>
    /// <param name = "name">
    /// 
    /// </param>
    /// <param name = "description">
    /// 
    /// </param>
    /// <param name = "sourceMap">
    /// 
    /// </param>
    public TypeDef WithInterface(string name, string? description = null, SourceMap? sourceMap = null)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name)));
        if (description is string description_)
        {
            arguments = arguments.Add(new Argument("description", new StringValue(description_)));
        }

        if (sourceMap is SourceMap sourceMap_)
        {
            arguments = arguments.Add(new Argument("sourceMap", new IdValue<SourceMapId>(sourceMap_)));
        }

        var queryBuilder = QueryBuilder.Select("withInterface", arguments);
        return new TypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Sets the kind of the type.
    /// </summary>
    /// <param name = "kind">
    /// 
    /// </param>
    public TypeDef WithKind(TypeDefKind kind)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("kind", new StringValue(kind.ToString())));
        var queryBuilder = QueryBuilder.Select("withKind", arguments);
        return new TypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Returns a TypeDef of kind List with the provided type for its elements.
    /// </summary>
    /// <param name = "elementType">
    /// 
    /// </param>
    public TypeDef WithListOf(TypeDef elementType)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("elementType", new IdValue<TypeDefId>(elementType)));
        var queryBuilder = QueryBuilder.Select("withListOf", arguments);
        return new TypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Returns a TypeDef of kind Object with the provided name.
    ///
    /// Note that an object's fields and functions may be omitted if the intent is only to refer to an object. This is how functions are able to return their own object, or any other circular reference.
    /// </summary>
    /// <param name = "name">
    /// 
    /// </param>
    /// <param name = "description">
    /// 
    /// </param>
    /// <param name = "sourceMap">
    /// 
    /// </param>
    /// <param name = "deprecated">
    /// 
    /// </param>
    public TypeDef WithObject(string name, string? description = null, SourceMap? sourceMap = null, string? deprecated = null)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name)));
        if (description is string description_)
        {
            arguments = arguments.Add(new Argument("description", new StringValue(description_)));
        }

        if (sourceMap is SourceMap sourceMap_)
        {
            arguments = arguments.Add(new Argument("sourceMap", new IdValue<SourceMapId>(sourceMap_)));
        }

        if (deprecated is string deprecated_)
        {
            arguments = arguments.Add(new Argument("deprecated", new StringValue(deprecated_)));
        }

        var queryBuilder = QueryBuilder.Select("withObject", arguments);
        return new TypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Sets whether this type can be set to null.
    /// </summary>
    /// <param name = "optional">
    /// 
    /// </param>
    public TypeDef WithOptional(bool optional)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("optional", new BooleanValue(optional)));
        var queryBuilder = QueryBuilder.Select("withOptional", arguments);
        return new TypeDef(queryBuilder, GraphQLClient);
    }

    /// <summary>
    /// Returns a TypeDef of kind Scalar with the provided name.
    /// </summary>
    /// <param name = "name">
    /// 
    /// </param>
    /// <param name = "description">
    /// 
    /// </param>
    public TypeDef WithScalar(string name, string? description = null)
    {
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("name", new StringValue(name)));
        if (description is string description_)
        {
            arguments = arguments.Add(new Argument("description", new StringValue(description_)));
        }

        var queryBuilder = QueryBuilder.Select("withScalar", arguments);
        return new TypeDef(queryBuilder, GraphQLClient);
    }
}

/// <summary>
/// The `TypeDefID` scalar type represents an identifier for an object of type TypeDef.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<TypeDefId>))]
public class TypeDefId : Scalar
{
}

/// <summary>
/// Distinguishes the different kinds of TypeDefs.
/// </summary>
[JsonConverter(typeof(JsonStringEnumConverter<TypeDefKind>))]
public enum TypeDefKind
{
    /// <summary>
    /// A string value.
    /// </summary>
    STRING_KIND,
    /// <summary>
    /// An integer value.
    /// </summary>
    INTEGER_KIND,
    /// <summary>
    /// A float value.
    /// </summary>
    FLOAT_KIND,
    /// <summary>
    /// A boolean value.
    /// </summary>
    BOOLEAN_KIND,
    /// <summary>
    /// A scalar value of any basic kind.
    /// </summary>
    SCALAR_KIND,
    /// <summary>
    /// Always paired with a ListTypeDef.
    ///
    /// A list of values all having the same type.
    /// </summary>
    LIST_KIND,
    /// <summary>
    /// Always paired with an ObjectTypeDef.
    ///
    /// A named type defined in the GraphQL schema, with fields and functions.
    /// </summary>
    OBJECT_KIND,
    /// <summary>
    /// Always paired with an InterfaceTypeDef.
    ///
    /// A named type of functions that can be matched+implemented by other objects+interfaces.
    /// </summary>
    INTERFACE_KIND,
    /// <summary>
    /// A graphql input type, used only when representing the core API via TypeDefs.
    /// </summary>
    INPUT_KIND,
    /// <summary>
    /// A special kind used to signify that no value is returned.
    ///
    /// This is used for functions that have no return value. The outer TypeDef specifying this Kind is always Optional, as the Void is never actually represented.
    /// </summary>
    VOID_KIND,
    /// <summary>
    /// A GraphQL enum type and its values
    ///
    /// Always paired with an EnumTypeDef.
    /// </summary>
    ENUM_KIND,
    /// <summary>
    /// A string value.
    /// </summary>
    STRING,
    /// <summary>
    /// An integer value.
    /// </summary>
    INTEGER,
    /// <summary>
    /// A float value.
    /// </summary>
    FLOAT,
    /// <summary>
    /// A boolean value.
    /// </summary>
    BOOLEAN,
    /// <summary>
    /// A scalar value of any basic kind.
    /// </summary>
    SCALAR,
    /// <summary>
    /// Always paired with a ListTypeDef.
    ///
    /// A list of values all having the same type.
    /// </summary>
    LIST,
    /// <summary>
    /// Always paired with an ObjectTypeDef.
    ///
    /// A named type defined in the GraphQL schema, with fields and functions.
    /// </summary>
    OBJECT,
    /// <summary>
    /// Always paired with an InterfaceTypeDef.
    ///
    /// A named type of functions that can be matched+implemented by other objects+interfaces.
    /// </summary>
    INTERFACE,
    /// <summary>
    /// A graphql input type, used only when representing the core API via TypeDefs.
    /// </summary>
    INPUT,
    /// <summary>
    /// A special kind used to signify that no value is returned.
    ///
    /// This is used for functions that have no return value. The outer TypeDef specifying this Kind is always Optional, as the Void is never actually represented.
    /// </summary>
    VOID,
    /// <summary>
    /// A GraphQL enum type and its values
    ///
    /// Always paired with an EnumTypeDef.
    /// </summary>
    ENUM
}

/// <summary>
/// The absence of a value.
///
/// A Null Void is used as a placeholder for resolvers that do not return anything.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<Void>))]
public class Void : Scalar
{
}