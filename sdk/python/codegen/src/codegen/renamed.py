"""Core schema names renamed for the engine's naming rules.

See hack/designs/identifier-casing.md, "Core schema". The engine still
accepts each old schema name as a hidden alias, but the schema doesn't show
it, so the deprecated aliases of the Python names derived from it are kept
from here.
"""

RENAMED_CORE_NAMES: dict[str, str] = {
    "Artifacts.filterURI": "filterUri",
    "Artifacts.withoutURI": "withoutUri",
    "FunctionCachePolicy.DEFAULT": "Default",
    "FunctionCachePolicy.NEVER": "Never",
    "FunctionCachePolicy.PER_SESSION": "PerSession",
    "GitCommit.parentSHAs": "parentShas",
    "GitCommit.shortSHA": "shortSha",
    "LLMContentBlock.callID": "callId",
    "LLMContentBlockInput.callID": "callId",
}
"""Old schema names by ``Type.name``: fields, input fields and enum values.

Arguments aren't listed: Python names don't change with them
(``pushURL`` and ``pushUrl`` are both ``push_url``).
"""


def legacy_core_name(type_name: str, name: str) -> str:
    """The schema name a core field or enum value had before it was renamed.

    That's the name itself, unless it's a renamed core name.
    """
    return RENAMED_CORE_NAMES.get(f"{type_name}.{name}", name)
