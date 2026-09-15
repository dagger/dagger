import typing

import dagger


@dagger.interface
class OtherIface(typing.Protocol):
    @dagger.function
    async def foo(self) -> str: ...


@dagger.interface
class CustomIface(typing.Protocol):
    @dagger.function
    async def void(self): ...

    @dagger.function
    async def str_(self) -> str: ...

    @dagger.function
    def with_str(self, str_arg: str) -> typing.Self: ...

    @dagger.function
    def with_optional_str(self, str_arg: str = "") -> typing.Self: ...

    @dagger.function
    async def str_list(self) -> list[str]: ...

    @dagger.function
    def with_str_list(self, str_list_arg: list[str]) -> typing.Self: ...

    @dagger.function
    async def int_(self) -> int: ...

    @dagger.function
    def with_int(self, int_arg: int) -> typing.Self: ...

    @dagger.function
    async def int_list(self) -> list[int]: ...

    @dagger.function
    def with_int_list(self, int_list_arg: list[int]) -> typing.Self: ...

    @dagger.function
    async def bool_(self) -> bool: ...

    @dagger.function
    def with_bool(self, bool_arg: bool) -> typing.Self: ...

    @dagger.function
    async def bool_list(self) -> list[bool]: ...

    @dagger.function
    def with_bool_list(self, bool_list_arg: list[bool]) -> typing.Self: ...

    @dagger.function
    def obj(self) -> dagger.Directory: ...

    @dagger.function
    def with_obj(self, obj_arg: dagger.Directory) -> typing.Self: ...

    @dagger.function
    def with_optional_obj(
        self, obj_arg: dagger.Directory | None = None
    ) -> typing.Self: ...

    @dagger.function
    async def obj_list(self) -> list[dagger.Directory]: ...

    @dagger.function
    def with_obj_list(self, obj_list_arg: list[dagger.Directory]) -> typing.Self: ...

    @dagger.function
    def self_iface(self) -> typing.Self: ...

    @dagger.function
    async def self_iface_list(self) -> list[typing.Self]: ...

    @dagger.function
    def other_iface(self) -> OtherIface: ...

    @dagger.function
    async def static_other_iface_list(self) -> list[OtherIface]: ...

    @dagger.function
    def with_other_iface(self, other: OtherIface) -> typing.Self: ...

    @dagger.function
    async def dynamic_other_iface_list(self) -> list[OtherIface]: ...

    @dagger.function
    def with_other_iface_by_iface(self, other: OtherIface) -> typing.Self: ...

    @dagger.function
    async def dynamic_other_iface_by_iface_list(self) -> list[OtherIface]: ...


@dagger.object_type
class OtherCustomObj:
    iface: CustomIface = dagger.field()
    iface_list: list[CustomIface] = dagger.field()


@dagger.object_type
class CustomObject:
    iface: CustomIface = dagger.field()
    iface_list: list[CustomIface] = dagger.field()
    other: OtherCustomObj = dagger.field()
    other_list: list[OtherCustomObj] = dagger.field()


@dagger.object_type
class Test:
    iface_field: CustomIface | None = dagger.field(default=None)
    iface_field_never_set: CustomIface | None = dagger.field(default=None)

    iface_private_field: CustomIface | None = None

    iface_list_field: list[CustomIface] = dagger.field(default=list)
    other_iface_list_field: list[OtherIface] = dagger.field(default=list)

    @dagger.function
    async def void(self, iface: CustomIface):
        await iface.void()

    @dagger.function
    async def str_(self, iface: CustomIface) -> str:
        return await iface.str_()

    @dagger.function
    def with_str(self, iface: CustomIface, str_arg: str) -> CustomIface:
        return iface.with_str(str_arg)

    @dagger.function
    def with_optional_str(self, iface: CustomIface, str_arg: str = "") -> CustomIface:
        return iface.with_optional_str(str_arg)

    @dagger.function
    async def str_list(self, iface_arg: CustomIface) -> list[str]:
        return await iface_arg.str_list()

    @dagger.function
    def with_str_list(self, iface: CustomIface, str_list: list[str]) -> CustomIface:
        return iface.with_str_list(str_list)

    @dagger.function
    async def int_(self, iface: CustomIface) -> int:
        return await iface.int_()

    @dagger.function
    def with_int(self, iface: CustomIface, int_arg: int) -> CustomIface:
        return iface.with_int(int_arg)

    @dagger.function
    async def int_list(self, iface_arg: CustomIface) -> list[int]:
        return await iface_arg.int_list()

    @dagger.function
    def with_int_list(self, iface: CustomIface, int_list: list[int]) -> CustomIface:
        return iface.with_int_list(int_list)

    @dagger.function
    async def bool_(self, iface: CustomIface) -> bool:
        return await iface.bool_()

    @dagger.function
    def with_bool(self, iface: CustomIface, bool_arg: bool) -> CustomIface:
        return iface.with_bool(bool_arg)

    @dagger.function
    async def bool_list(self, iface_arg: CustomIface) -> list[bool]:
        return await iface_arg.bool_list()

    @dagger.function
    def with_bool_list(self, iface: CustomIface, bool_list: list[bool]) -> CustomIface:
        return iface.with_bool_list(bool_list)

    @dagger.function
    def obj(self, iface: CustomIface) -> dagger.Directory:
        return iface.obj()

    @dagger.function
    def with_obj(self, iface: CustomIface, obj: dagger.Directory) -> CustomIface:
        return iface.with_obj(obj)

    @dagger.function
    def with_optional_obj(
        self, iface: CustomIface, obj: dagger.Directory | None = None
    ) -> CustomIface:
        return iface.with_optional_obj(obj)

    @dagger.function
    async def obj_list(self, iface: CustomIface) -> list[dagger.Directory]:
        return await iface.obj_list()

    @dagger.function
    def with_obj_list(
        self, iface: CustomIface, obj_list: list[dagger.Directory]
    ) -> CustomIface:
        return iface.with_obj_list(obj_list)

    @dagger.function
    def self_iface(self, iface: CustomIface) -> CustomIface:
        return iface.self_iface()

    @dagger.function
    async def self_iface_list(self, iface: CustomIface) -> list[CustomIface]:
        return await iface.self_iface_list()

    @dagger.function
    def other_iface(self, iface: CustomIface) -> OtherIface:
        return iface.other_iface()

    @dagger.function
    async def static_other_iface_list(self, iface: CustomIface) -> list[OtherIface]:
        return await iface.static_other_iface_list()

    @dagger.function
    def with_other_iface(self, iface: CustomIface, other: OtherIface) -> CustomIface:
        return iface.with_other_iface(other)

    @dagger.function
    async def dynamic_other_iface_list(self, iface: CustomIface) -> list[OtherIface]:
        return await iface.dynamic_other_iface_list()

    @dagger.function
    def with_other_iface_by_iface(
        self, iface: CustomIface, other: OtherIface
    ) -> CustomIface:
        return iface.with_other_iface_by_iface(other)

    @dagger.function
    async def dynamic_other_iface_by_iface_list(
        self, iface: CustomIface
    ) -> list[OtherIface]:
        return await iface.dynamic_other_iface_by_iface_list()

    @dagger.function
    async def iface_list_args(
        self, ifaces: list[CustomIface], other_ifaces: list[OtherIface]
    ) -> list[str]:
        return [await iface.str_() for iface in ifaces] + [
            await iface.foo() for iface in other_ifaces
        ]

    @dagger.function
    def with_iface(self, iface: CustomIface) -> typing.Self:
        self.iface_field = iface
        return self

    @dagger.function
    def with_optional_iface(self, iface: CustomIface | None = None) -> typing.Self:
        if iface is not None:
            self.iface_field = iface
        return self

    @dagger.function
    def with_iface_list(self, ifaces: list[CustomIface]) -> typing.Self:
        self.iface_list_field = ifaces
        return self

    @dagger.function
    def with_other_iface_list(self, other_ifaces: list[OtherIface]) -> typing.Self:
        self.other_iface_list_field = other_ifaces
        return self

    @dagger.function
    def with_private_iface(self, iface: CustomIface) -> typing.Self:
        self.iface_private_field = iface
        return self

    @dagger.function
    async def parent_iface_fields(self) -> list[str]:
        strs: list[str] = []

        if self.iface_field is not None:
            strs.append(await self.iface_field.str_())

        if self.iface_private_field is not None:
            strs.append(await self.iface_private_field.str_())

        strs.extend([await iface.str_() for iface in self.iface_list_field])
        strs.extend([await iface.foo() for iface in self.other_iface_list_field])

        return strs

    @dagger.function
    def return_custom_obj(
        self,
        ifaces: list[CustomIface],
        other_ifaces: list[OtherIface],
    ) -> CustomObject:
        return CustomObject(
            iface=ifaces[0],
            iface_list=ifaces,
            other=OtherCustomObj(iface=ifaces[0], iface_list=ifaces),
            other_list=[
                OtherCustomObj(iface=ifaces[0], iface_list=ifaces),
            ],
        )
