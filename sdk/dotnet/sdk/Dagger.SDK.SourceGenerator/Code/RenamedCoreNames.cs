using System.Collections.Generic;

namespace Dagger.SDK.SourceGenerator.Code;

/// <summary>
/// Core schema names renamed for the engine's naming rules (see
/// hack/designs/identifier-casing.md, "Core schema", in the Dagger
/// repository), with their old schema names. The engine still accepts the
/// old names as hidden aliases, but the schema doesn't show them, so the C#
/// names derived from them are kept from here: the obsolete forwarders,
/// properties and enum members named after a schema name are named after
/// the old one.
/// </summary>
public static class RenamedCoreNames
{
    // Old schema names by "Type.name": fields, input object fields and enum
    // values. Arguments aren't listed: camelCase parameters spell the old
    // and new names alike.
    private static readonly Dictionary<string, string> Names = new()
    {
        ["Artifacts.filterURI"] = "filterUri",
        ["Artifacts.withoutURI"] = "withoutUri",
        ["FunctionCachePolicy.DEFAULT"] = "Default",
        ["FunctionCachePolicy.NEVER"] = "Never",
        ["FunctionCachePolicy.PER_SESSION"] = "PerSession",
        ["GitCommit.parentSHAs"] = "parentShas",
        ["GitCommit.shortSHA"] = "shortSha",
        ["LLMContentBlock.callID"] = "callId",
        ["LLMContentBlockInput.callID"] = "callId",
    };

    /// <summary>
    /// The schema name a field, input object field or enum value of a type
    /// had before it was renamed: the name itself, unless it's a renamed
    /// core name.
    /// </summary>
    public static string Legacy(string typeName, string name) =>
        Names.TryGetValue($"{typeName}.{name}", out var legacy) ? legacy : name;
}
