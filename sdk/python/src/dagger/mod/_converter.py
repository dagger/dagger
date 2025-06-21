import enum
import functools
import inspect
import logging
import typing

from beartype.door import TypeHint
from cattrs.preconf.json import make_converter as make_json_converter

import dagger
from dagger import dag
from dagger.client._core import Arg, configure_converter_enum
from dagger.client._guards import is_id_type, is_id_type_subclass
from dagger.client.base import Interface, Scalar, Type
from dagger.mod._resolver import Function
from dagger.mod._utils import (
    get_doc,
    get_module,
    get_object_type,
    is_annotated,
    is_dagger_interface_type,
    is_dagger_object_type,
    is_initvar,
    is_nullable,
    is_subclass,
    is_union,
    list_of,
    non_null,
    strip_annotations,
    syncify,
    to_camel_case,
)

logger = logging.getLogger(__name__)

if typing.TYPE_CHECKING:
    from dagger import TypeDef


def make_converter():
    conv = make_json_converter(
        detailed_validation=True,
    )

    conv.register_structure_hook_func(
        is_id_type_subclass,
        dagger_type_structure,
    )
    conv.register_unstructure_hook_func(
        lambda t: is_id_type_subclass(t) or is_dagger_interface_type(t),
        dagger_type_unstructure,
    )

    conv.register_structure_hook_func(
        is_dagger_interface_type,
        dagger_interface_structure,
    )

    configure_converter_enum(conv)

    return conv


def dagger_type_structure(id_: str | Scalar, cls: type[Type]):
    """Get dagger object type from id."""
    cls = strip_annotations(cls)

    if not is_id_type_subclass(cls) and not is_dagger_interface_type(cls):
        msg = f"Unsupported type '{cls.__name__}'"
        raise TypeError(msg)

    return cls(
        dag._select(f"load{cls._graphql_name()}FromID", [Arg("id", id_)])  # noqa: SLF001
    )


def dagger_interface_structure(id_, cls: type[Interface]):
    """Get dagger interface implementation from id."""
    return dagger_type_structure(id_, to_interface_impl(cls))


def dagger_type_unstructure(obj):
    """Get id from dagger object."""
    if not is_id_type(obj) and not isinstance(obj, Interface):
        msg = f"Expected dagger Type object, got `{type(obj)}`"
        raise TypeError(msg)
    return syncify(obj.id)


@functools.cache
def to_interface_impl(proto: type) -> type[Interface]:
    """Return a dynamically generated client binding for the interface."""
    typ = get_object_type(proto)
    mod = get_module(proto)

    if typ is None or not typ.interface or mod is None:
        msg = f"Unexpected interface type `{proto}`"
        raise TypeError(msg)

    methods = {
        func.original_name: make_method(name, func, proto)
        for name, func in typ.functions.items()
    }

    return type(
        mod.main_cls.__name__ + proto.__name__,
        (Interface,),
        {"_declaration": proto, **methods},
    )


def make_method(name: str, func: Function, proto: type) -> typing.Callable:  # noqa: C901
    """Generate method for interface client binding."""
    ret_type = func.return_type
    _is_self = ret_type is proto

    if not _is_self and is_dagger_interface_type(ret_type):
        ret_type = to_interface_impl(ret_type)

    # Need to convert names to GraphQL convention for query builder
    gql_name = to_camel_case(name)
    gql_arg_names = {
        param.name: to_camel_case(param.name) for param in func.parameters.values()
    }

    # Generate query builder selection based on inputs
    def select(obj: Interface, *args, **kwargs):
        bound = func.signature.bind(obj, *args, **kwargs)
        args = [
            Arg(name=gql_arg_names[arg_name], value=arg_value)
            for arg_name, arg_value in bound.arguments.items()
            if arg_name != "self"
        ]
        return obj._select(gql_name, args)  # noqa: SLF001

    # Mimic function signature defined in the interface
    def wrap(c: typing.Callable):
        c.__signature__ = func.signature
        return functools.wraps(func.wrapped)(c)

    # If return type is an object, then it's a lazy/chain method (sync)
    if _is_self or is_dagger_object_type(ret_type):

        def chain_method(self, *args, **kwargs):
            _ctx = select(self, *args, **kwargs)
            if _is_self:
                # we don't have a finished type yet but we can use self
                return type(self)(_ctx)
            return ret_type(_ctx)

        return wrap(chain_method)

    # Anything else triggers execution (async)
    async def exec_method(self, *args, **kwargs):
        _ctx = select(self, *args, **kwargs)
        if cls := list_of(ret_type):
            if cls is proto:
                cls = type(self)
            elif is_dagger_interface_type(cls):
                cls = to_interface_impl(cls)
            if is_dagger_object_type(cls):
                return await _ctx.execute_object_list(cls)
        return await _ctx.execute(ret_type)

    return wrap(exec_method)


@functools.cache
def to_typedef(annotation: typing.Any) -> "TypeDef":  # noqa: C901, PLR0911
    """Convert Python object to API type."""
    if is_initvar(annotation):
        return to_typedef(annotation.type)

    if is_annotated(annotation):
        return to_typedef(strip_annotations(annotation))

    td = dag.type_def()

    typ = TypeHint(annotation)

    if is_nullable(typ):
        td = td.with_optional(True)

    typ = non_null(typ)

    # Can't represent unions in the API.
    if is_union(typ):
        msg = f"Unsupported union type: {typ.hint}"
        raise TypeError(msg)

    builtins = {
        str: dagger.TypeDefKind.STRING_KIND,
        int: dagger.TypeDefKind.INTEGER_KIND,
        float: dagger.TypeDefKind.FLOAT_KIND,
        bool: dagger.TypeDefKind.BOOLEAN_KIND,
        type(None): dagger.TypeDefKind.VOID_KIND,
    }

    if typ.hint in builtins:
        return td.with_kind(builtins[typ.hint])

    if el := list_of(typ.hint):
        return td.with_list_of(to_typedef(el))

    if inspect.isclass(cls := typ.hint):
        name = cls.__name__

        if is_subclass(cls, enum.Enum):
            return td.with_enum(name, description=get_doc(cls))

        if is_subclass(cls, Scalar):
            return td.with_scalar(name, description=get_doc(cls))

        # object defined in this module
        if obj_type := get_object_type(cls):
            if obj_type.interface:
                return td.with_interface(name)
            return td.with_object(name)

        # object type from API (codegen)
        if is_id_type_subclass(cls):
            return td.with_object(name)

    msg = f"Unsupported type: {typ.hint!r}"
    raise TypeError(msg)
