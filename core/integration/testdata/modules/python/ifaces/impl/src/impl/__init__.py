import dataclasses
import typing

import dagger


@dagger.object_type
class OtherImpl:
    foo: str = dagger.field()


@dagger.interface
class LocalOtherOface(typing.Protocol):
    # LocalOtherIface is the same as OtherIface and is used here to test interface
    # to interface compatibility."""

    @dagger.function
    def foo(self) -> str: ...


@dagger.object_type
class Impl:
    str_: str = dagger.field(init=False)
    strs: list[str] = dagger.field()

    int_: int = dagger.field(init=False)
    ints: list[int] = dagger.field()

    bool_: bool = dagger.field(init=False)
    bools: list[bool] = dagger.field()

    obj: dagger.Directory = dagger.field(init=False)
    objs: list[dagger.Directory] = dagger.field()

    others: list[OtherImpl] = dagger.field(init=False, default=list)
    other_ifaces: list[LocalOtherOface] = dagger.field(init=False, default=list)

    def __post_init__(self):
        self.str_ = self.strs[0]
        self.int_ = self.ints[0]
        self.bool_ = self.bools[0]
        self.obj = self.objs[0]

    @dagger.function
    def void(self): ...

    @dagger.function
    def with_str(self, str_arg: str) -> typing.Self:
        # replace for local immutability
        return dataclasses.replace(self, str_=str_arg)

    @dagger.function
    def with_str_list(self, str_list: list[str]) -> typing.Self:
        self.strs = str_list
        return self

    @dagger.function
    def with_int(self, int_arg: int) -> typing.Self:
        self.int_ = int_arg
        return self

    @dagger.function
    def with_int_list(self, int_list: list[int]) -> typing.Self:
        self.ints = int_list
        return self

    @dagger.function
    def with_bool(self, bool_arg: bool) -> typing.Self:
        self.bool_ = bool_arg
        return self

    @dagger.function
    def with_obj(self, obj: dagger.Directory | None = None) -> typing.Self:
        if obj is not None:
            self.obj = obj
        return self

    @dagger.function
    def with_obj_list(self, obj_list: list[dagger.Directory]) -> typing.Self:
        self.objs = obj_list
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
