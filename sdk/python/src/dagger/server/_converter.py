import logging
from typing import (
    Annotated,
    NotRequired,
    Protocol,
    TypedDict,
    get_args,
    get_origin,
    runtime_checkable,
)

import anyio
from anyio.abc import TaskGroup
from cattrs.preconf.json import make_converter as make_json_converter

from ._utils import asyncify, syncify

logger = logging.getLogger(__name__)


BUILTINS = {
    str: "String",
    int: "Int",
    float: "Float",
    bool: "Boolean",
}


@runtime_checkable
class GraphQLNamed(Protocol):
    @classmethod
    def graphql_name(cls) -> str:
        ...


class CheckResult(TypedDict):
    name: NotRequired[str]
    success: bool
    output: str
    subresults: NotRequired[list["CheckResult"]]


def make_converter():
    import dagger
    from dagger.client._guards import is_id_type_subclass

    conv = make_json_converter(omit_if_default=True)

    # TODO: register cache volume for custom handling since it's different
    # than the other types.

    def dagger_type_structure(id_, cls):
        """Get dagger object type from id."""
        return dagger.client()._get_object_instance(id_, cls)  # noqa: SLF001

    def dagger_type_unstructure(obj):
        """Get id from dagger object."""
        return asyncify(obj.id)

    conv.register_structure_hook_func(
        is_id_type_subclass,
        dagger_type_structure,
    )

    conv.register_unstructure_hook_func(
        is_id_type_subclass,
        dagger_type_unstructure,
    )

    async def check_result_unstructure(result: dagger.EnvironmentCheckResult):
        """Serialize a dagger.EnvironmentCheckResult recursively."""
        data: CheckResult = {"success": False, "output": ""}

        async def _update_attr(key: str):
            data[key] = await getattr(result, key)()

        async def _add_subresult(subresult: dagger.EnvironmentCheckResult):
            if "subresults" not in data:
                data["subresults"] = []
            data["subresults"].append(await check_result_unstructure(subresult))

        async def _get_subresults(tg: TaskGroup):
            for subresult in await result.subresults():
                tg.start_soon(_add_subresult, subresult)

        async with anyio.create_task_group() as tg:
            tg.start_soon(_update_attr, "name")
            tg.start_soon(_update_attr, "success")
            tg.start_soon(_update_attr, "output")
            tg.start_soon(_get_subresults, tg)

        return data

    conv.register_unstructure_hook(
        dagger.EnvironmentCheckResult,
        lambda r: syncify(check_result_unstructure, r),
    )

    return conv


def to_graphql_representation(obj) -> str:
    """Convert object to GraphQL type as a string."""
    if get_origin(obj) is Annotated:
        # Only support the first argument when annotated.
        obj, *_ = get_args(obj)
    if obj in BUILTINS:
        return BUILTINS[obj]
    return obj.graphql_name() if isinstance(obj, GraphQLNamed) else ""
