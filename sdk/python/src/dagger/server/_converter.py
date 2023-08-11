import logging
from typing import (
    Annotated,
    NotRequired,
    TypedDict,
    cast,
    get_args,
    get_origin,
)

import anyio
from anyio.abc import TaskGroup
from cattrs.preconf.json import make_converter as make_json_converter

from ._utils import syncify

logger = logging.getLogger(__name__)


BUILTINS = {
    str: "String",
    int: "Int",
    float: "Float",
    bool: "Boolean",
}


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
        return syncify(obj.id)

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


# TODO: dedupe
def to_graphql_input_representation(type_) -> str:
    if get_origin(type_) is Annotated:
        # Only support the first argument when annotated.
        type_, *_ = get_args(type_)

    if type_ in BUILTINS:
        return BUILTINS[type_]

    from dagger.client.base import Scalar, Type

    if issubclass(type_, Type) and hasattr(type_, "_id_type"):
        return cast(type[Scalar], type_._id_type()).__name__  # noqa: SLF001

    logger.warning(
        "Could not convert output type  %s to GraphQL representation.", type_
    )
    # TODO: raise error instead?
    return ""


def to_graphql_output_representation(type_) -> str:
    """Convert result type to GraphQL type as a string."""
    if get_origin(type_) is Annotated:
        # Only support the first argument when annotated.
        type_, *_ = get_args(type_)

    if type_ in BUILTINS:
        return BUILTINS[type_]

    from dagger.client.base import Type

    if issubclass(type_, Type):
        return type_._graphql_name()  # noqa: SLF001

    logger.warning(
        "Could not convert output type  %s to GraphQL representation.", type_
    )
    return ""
