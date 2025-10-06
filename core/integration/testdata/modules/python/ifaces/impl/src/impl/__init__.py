import dataclasses
import typing

import dagger


@dagger.object_type
class OtherImpl:
    foo: str = dagger.field()


@dagger.interface
class LocalOtherIface(typing.Protocol):
    # LocalOtherIface is the same as OtherIface and is used here to test interface
    # to interface compatibility."""

    @dagger.function
    def foo(self) -> str: ...


@dagger.object_type
class Impl:
    str_: str = dagger.field(default="")
    str_list: list[str] = dagger.field()

    int_: int = dagger.field(init=False)
    int_list: list[int] = dagger.field()

    bool_: bool = dagger.field(init=False)
    bool_list: list[bool] = dagger.field()

    obj: dagger.Directory = dagger.field(init=False)
    obj_list: list[dagger.Directory] = dagger.field()

    others: list[OtherImpl] = dagger.field(init=False, default=list)
    other_ifaces: list[LocalOtherIface] = dagger.field(init=False, default=list)

    def __post_init__(self):
        if self.str_ == "":
            self.str_ = self.str_list[0]
        self.int_ = self.int_list[0]
        self.bool_ = self.bool_list[0]
        self.obj = self.obj_list[0]

    @dagger.function
    def void(self): ...

    @dagger.function
    def with_str(self, str_arg: str) -> typing.Self:
        # replace for local immutability since it's being used in other methods
        return dataclasses.replace(self, str_=str_arg)

    @dagger.function
    def with_optional_str(self, str_arg: str = "") -> typing.Self:
        if str_arg != "":
            return self.with_str(str_arg)
        return self

    @dagger.function
    def with_str_list(self, str_list_arg: list[str]) -> typing.Self:
        self.str_list = str_list_arg
        return self

    @dagger.function
    def with_int(self, int_arg: int) -> typing.Self:
        self.int_ = int_arg
        return self

    @dagger.function
    def with_int_list(self, int_list_arg: list[int]) -> typing.Self:
        self.int_list = int_list_arg
        return self

    @dagger.function
    def with_bool(self, bool_arg: bool) -> typing.Self:
        self.bool_ = bool_arg
        return self

    @dagger.function
    def with_bool_list(self, bool_list_arg: list[bool]) -> typing.Self:
        self.bool_list = bool_list_arg
        return self

    @dagger.function
    def with_obj(self, obj_arg: dagger.Directory) -> typing.Self:
        self.obj = obj_arg
        return self

    @dagger.function
    def with_optional_obj(self, obj_arg: dagger.Directory | None = None) -> typing.Self:
        if obj_arg is not None:
            return self.with_obj(obj_arg)
        return self

    @dagger.function
    def with_obj_list(self, obj_list_arg: list[dagger.Directory]) -> typing.Self:
        self.obj_list = obj_list_arg
        return self

    @dagger.function
    def self_iface(self) -> typing.Self:
        return self.with_str(self.str_ + "self")

    @dagger.function
    def self_iface_list(self) -> list[typing.Self]:
        return [
            self.with_str(self.str_ + "self1"),
            self.with_str(self.str_ + "self2"),
        ]

    @dagger.function
    def other_iface(self) -> OtherImpl:
        return OtherImpl(foo=self.str_ + "other")

    @dagger.function
    def static_other_iface_list(self) -> list[OtherImpl]:
        return [
            OtherImpl(foo=self.str_ + "other1"),
            OtherImpl(foo=self.str_ + "other2"),
        ]

    @dagger.function
    def with_other_iface(self, other: OtherImpl) -> typing.Self:
        self.others.append(other)
        return self

    @dagger.function
    def dynamic_other_iface_list(self) -> list[OtherImpl]:
        return self.others

    @dagger.function
    def with_other_iface_by_iface(self, other: LocalOtherIface) -> typing.Self:
        self.other_ifaces.append(other)
        return self

    @dagger.function
    def dynamic_other_iface_by_iface_list(self) -> list[LocalOtherIface]:
        return self.other_ifaces
