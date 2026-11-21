import enum
import functools
import logging
import typing

from cattrs.preconf.json import make_converter as make_json_converter

from dagger.client._core import Arg, Context, EnumName, configure_converter_enum
from dagger.client._guards import is_id_type, is_id_type_subclass
from dagger.client.base import Interface, Scalar, Type
from dagger.mod import _api
from dagger.mod._describe import TypeRef, describe_type
from dagger.mod._resolver import Function
from dagger.mod._utils import (
    get_module,
    get_object_type,
    is_dagger_interface_type,
    is_dagger_object_type,
    list_of,
    non_null,
    strip_annotations,
    syncify,
)

if typing.TYPE_CHECKING:
    from dagger.mod._module import Module

logger = logging.getLogger(__name__)


def make_converter():
    conv = make_json_converter()

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

    return cls(Context().select_id(cls._graphql_name(), id_))


def dagger_interface_structure(id_, cls: type[Interface]):
    """Get dagger interface implementation from id."""
    return dagger_type_structure(id_, to_interface_impl(cls))


def dagger_type_unstructure(obj):
    """Get id from dagger object."""
    if not is_id_type(obj) and not isinstance(obj, Interface):
        msg = f"Expected dagger Type object, got '{type(obj)}'"
        raise TypeError(msg)
    return syncify(obj.id)


@functools.cache
def to_interface_impl(proto: type) -> type[Interface]:
    """Return a dynamically generated client binding for the interface."""
    typ = get_object_type(proto)
    mod = get_module(proto)

    if typ is None or not typ.interface or mod is None:
        msg = f"Unexpected interface type '{proto}'"
        raise TypeError(msg)

    methods = {
        func.original_name: make_method(name, func, proto, mod)
        for name, func in typ.functions.items()
    }

    # The class name is the interface's schema name: it's what the client
    # selects the type by (see `Type._graphql_name`).
    return type(
        mod.names.type_name(proto.__name__),
        (Interface,),
        {"_declaration": proto, **methods},
    )


def make_method(  # noqa: C901
    name: str,
    func: Function,
    proto: type,
    mod: "Module",
) -> typing.Callable:
    """Generate method for interface client binding."""
    ret_type = func.return_type
    _is_self = ret_type is proto

    if not _is_self and is_dagger_interface_type(ret_type):
        ret_type = to_interface_impl(ret_type)

    # The interface's field and argument names, as the engine names them.
    names = mod.names
    gql_name = names.field_name(name)
    # Keyed by Python name, which is what the call binds.
    gql_arg_names = {
        py_name: names.field_name(param.name)
        for py_name, param in func.parameters.items()
    }

    def wire(value: typing.Any) -> typing.Any:
        """A value as it goes over the wire, by the engine's names.

        A member of one of the module's own enums is sent by the name the
        engine gave it (``arm64`` is ``ARM64``); the client sends anything
        else as it always does, a member by its Python name.
        """
        if isinstance(value, list | tuple):
            return [wire(v) for v in value]
        if isinstance(value, enum.Enum) and mod.is_own_enum(type(value)):
            return EnumName(names.enum_member_name(value.name))
        return value

    def own_enum(t: typing.Any) -> type[enum.Enum] | None:
        """The module's own enum a result is, or None."""
        t = non_null(strip_annotations(t))
        return t if mod.is_own_enum(t) else None

    def read(cls: type[enum.Enum], value: str | None) -> enum.Enum | None:
        """A member of one of the module's own enums, from its engine name."""
        if value is None:
            return None
        members = {names.enum_member_name(m.name): m for m in cls}
        try:
            return members[value]
        except KeyError:
            msg = f"Unexpected member {value!r} of enum '{cls.__name__}'"
            raise TypeError(msg) from None

    # Generate query builder selection based on inputs
    def select(obj: Interface, *args, **kwargs):
        bound = func.signature.bind(obj, *args, **kwargs)
        args = [
            Arg(name=gql_arg_names[arg_name], value=wire(arg_value))
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
        # The module's own enums come back by the names the engine gave them.
        if enum_cls := own_enum(ret_type):
            return read(enum_cls, await _ctx.execute(str | None))
        if cls := list_of(ret_type):
            if enum_cls := own_enum(cls):
                values = await _ctx.execute(list[str | None])
                return [read(enum_cls, v) for v in values]
            if cls is proto:
                cls = type(self)
            elif is_dagger_interface_type(cls):
                cls = to_interface_impl(cls)
            if is_dagger_object_type(cls):
                return await _ctx.execute_object_list(cls)
        return await _ctx.execute(ret_type)

    return wrap(exec_method)


@functools.cache
def to_typedef(annotation: typing.Any, context: str = "type") -> _api.TypeDef:
    """Convert Python object to API type."""
    return typedef_from(describe_type(annotation, context))


def typedef_from(ref: TypeRef) -> _api.TypeDef:
    """Build the API type from its description."""
    td = _api.type_def()

    if ref.optional:
        td = td.with_optional(True)

    match ref.kind:
        case "LIST_KIND":
            assert ref.elem is not None
            return td.with_list_of(typedef_from(ref.elem))
        case "ENUM_KIND":
            return td.with_enum(ref.name, description=ref.description)
        case "SCALAR_KIND":
            return td.with_scalar(ref.name, description=ref.description)
        case "INTERFACE_KIND":
            return td.with_interface(ref.name)
        case "OBJECT_KIND":
            return td.with_object(ref.name)
        case _:
            return td.with_kind(ref.kind)
