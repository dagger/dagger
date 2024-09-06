import dataclasses
import inspect

from typing_extensions import deprecated

from dagger.mod._types import APIName, ContextPath


@dataclasses.dataclass(slots=True, frozen=True)
class Name:
    """An alternative name when exposing a function argument to the API.

    Useful to avoid conflicts with reserved words.

    Example usage::

        @function
        def pull(from_: Annotated[str, Name("from")]): ...
    """

    name: APIName

    def __str__(self) -> str:
        return self.name


@deprecated("Arg is deprecated, use Name instead.")
class Arg(Name):
    """An alternative name when exposing a function argument to the API.

    .. deprecated::
        Use :py:class:`Name` instead.
    """


@dataclasses.dataclass(slots=True, frozen=True)
class DefaultPath:
    """If the argument is omitted, load it from the given path in the context directory.

    Only applies to arguments of type :py:class:`dagger.Directory` or
    :py:class:`dagger.File`.

    Mutually exclusive with setting a default value for the parameter. When
    used within Python, the parameter should be required.

    Example usage::

        @function
        def build(src: Annotated[dagger.Directory, DefaultPath("..")]): ...
    """

    from_context: ContextPath

    def __str__(self) -> str:
        return self.from_context


@dataclasses.dataclass(slots=True, frozen=True)
class Ignore:
    """Ignore patterns for :py:class:`dagger.Directory` arguments.

    The ignore patterns are applied to the input directory, and matching entries
    are filtered out, in a cache-efficient manner.

    Useful if it's known in advance which files or directories should be
    excluded when loading the directory.

    Example usage::

        @function
        def build(src: Annotated[dagger.Directory, Ignore([".venv"])]): ...
    """

    patterns: list[str]

    # TODO: to allow frozen=True, the patterns can't be in a list (mutable),
    # but changing it to an immutable sequence now will produce IDE errors
    # for users which requires a change to their existing code. It's not that
    # important to be immutable though, just for future consideration.
    def __hash__(self) -> int:
        return hash(tuple(self.patterns))


@dataclasses.dataclass(slots=True, frozen=True, kw_only=True)
class Parameter:
    """Parameter from function signature in :py:class:`FunctionResolver`."""

    name: APIName

    # Inspect
    signature: inspect.Parameter
    resolved_type: type
    is_nullable: bool

    # Metadata
    doc: str | None
    ignore: Ignore | None = None
    default_path: DefaultPath | None = None

    @property
    def has_default(self) -> bool:
        return self.signature.default is not inspect.Parameter.empty

    @property
    def is_optional(self) -> bool:
        return self.has_default or self.default_path is not None or self.is_nullable
