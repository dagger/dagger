import dataclasses
import inspect
import logging

from cattrs.preconf.json import JsonConverter

import dagger
from dagger.mod._exceptions import BadUsageError
from dagger.mod._types import APIName, ContextPath

logger = logging.getLogger(__name__)


@dataclasses.dataclass(slots=True, frozen=True)
class Name:
    """An alternative name when exposing a function argument to the API.

    Useful to avoid conflicts with reserved words.

    Example usage::

        @function
        def pull(self, from_: Annotated[str, Name("from")]): ...
    """

    name: APIName

    def __str__(self) -> str:
        return self.name


@dataclasses.dataclass(slots=True, frozen=True)
class DefaultPath:
    """If the argument is omitted, load it from the given path in the context directory.

    Only applies to arguments of type
    :py:class:`dagger.Directory`/:py:class:`dagger.File`
    or :py:class:`dagger.GitRepository`/:py:class:`dagger.GitRef`.


    Mutually exclusive with setting a default value for the parameter. When
    used within Python, the parameter should be required.

    Example usage::

        @function
        def build(self, src: Annotated[dagger.Directory, DefaultPath("..")]): ...


        @function
        def build(self, src: Annotated[dagger.GitRef, DefaultPath("./.git")]): ...
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
        def build(self, src: Annotated[dagger.Directory, Ignore([".venv"])]): ...
    """

    patterns: list[str]

    # TODO: to allow frozen=True, the patterns can't be in a list (mutable),
    # but changing it to an immutable sequence now will produce IDE errors
    # for users which requires a change to their existing code. It's not that
    # important to be immutable though, just for future consideration.
    def __hash__(self) -> int:
        return hash(tuple(self.patterns))


@dataclasses.dataclass(slots=True, frozen=True)
class Deprecated:
    """Mark a function argument as deprecated.

    Example usage::

        @function
        def old(self, value: Annotated[str, Deprecated("Use new instead")]): ...
    """

    reason: str = ""

    def __str__(self) -> str:
        return self.reason


@dataclasses.dataclass(slots=True, kw_only=True)
class Parameter:
    """Parameter from function signature in :py:class:`FunctionResolver`."""

    name: APIName

    # Inspect
    signature: inspect.Parameter
    resolved_type: type
    is_nullable: bool

    # Metadata
    doc: str | None = None
    ignore: list[str] | None = None
    default_path: ContextPath | None = None
    default_value: dagger.JSON | None = None
    deprecated: str | None = None

    conv: dataclasses.InitVar[JsonConverter]

    def __post_init__(self, conv: JsonConverter):
        self._validate()

        if not self.has_default:
            return
        try:
            self.default_value = dagger.JSON(conv.dumps(self.signature.default))
        except TypeError as e:
            # Rather than failing on a default value that's not JSON
            # serializable and going through hoops to support more and more
            # types, just don't register it. It'll still be registered
            # as optional so the API server will call the function without
            # it and let Python handle it.
            logger.debug(
                "Not registering default value for %s: %s",
                self.signature,
                e,
            )
            self.is_nullable = True

    @property
    def has_default(self) -> bool:
        return self.signature.default is not inspect.Parameter.empty

    @property
    def is_optional(self) -> bool:
        return any(
            [
                self.has_default,
                self.default_path is not None,
                self.is_nullable,
            ]
        )

    def _validate(self):
        extra = {"parameter": self.signature}

        # These validations are already done by the engine, just repeating them
        # here for better error messages.
        if not self.is_nullable and self.has_default and self.signature.default is None:
            msg = (
                "Can't use a default value of None on a non-nullable type for "
                f"parameter '{self.signature.name}'"
            )
            raise BadUsageError(msg, extra=extra)

        if self.default_path:
            if self.has_default and not (
                self.is_nullable and self.signature.default is None
            ):
                msg = (
                    f"DefaultPath can't be used in parameter '{self.signature.name}' "
                    "since it already defines a default value."
                )
                raise BadUsageError(msg, extra=extra)

            if not self.default_path:
                # NB: We could instead warn or just ignore, but it's better to fail
                # fast to avoid astonishment.
                msg = (
                    "DefaultPath can't be used with an empty path in "
                    f"parameter '{self.signature.name}'"
                )
                raise BadUsageError(msg, extra=extra)

        if self.deprecated and not self.is_optional:
            msg = (
                f"Can't deprecate required parameter '{self.signature.name}'. "
                "Mark it optional or provide a default value."
            )
            raise BadUsageError(msg, extra=extra)
