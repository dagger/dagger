import abc
import logging
from collections import UserDict
from typing import TypeVar

from attrs import define

from dagger.config import Config
from dagger.exceptions import DaggerError

logger = logging.getLogger(__name__)


@define
class Engine(abc.ABC):
    """Base class to start engine, with provisioning if needed."""

    cfg: Config
    """Configuration for connector. Can be mutated."""

    @abc.abstractmethod
    async def start(self) -> None:
        ...

    @abc.abstractmethod
    async def stop(self, exc_type) -> None:
        ...

    async def __aenter__(self):
        await self.start()
        return self

    async def __aexit__(self, exc_type, *args, **kwargs):
        await self.stop(exc_type)

    @abc.abstractmethod
    def start_sync(self) -> None:
        ...

    @abc.abstractmethod
    def stop_sync(self, exc_type) -> None:
        ...

    def __enter__(self):
        self.start_sync()
        return self

    def __exit__(self, exc_type, *args, **kwargs):
        self.stop_sync(exc_type)


class NoopEngine(Engine):
    """A no op to make implementation simpler by assuming there's
    always a provisioner."""

    async def start(self) -> None:
        ...

    async def stop(self, exc_type) -> None:
        ...

    def start_sync(self) -> None:
        ...

    def stop_sync(self, exc_type) -> None:
        ...


_RT = TypeVar("_RT", bound=type)


class _Registry(UserDict[str, type[Engine]]):
    def add(self, scheme: str):
        def register(cls: _RT) -> _RT:
            if scheme not in self.data:
                if not issubclass(cls, Engine):
                    raise TypeError(f"{cls.__name__} isn't an Engine subclass")
                self.data[scheme] = cls
            elif cls is not self.data[scheme]:
                logger.debug(f"Replaced {scheme} provisioner with {cls}")
            return cls

        return register

    def get_(self, cfg: Config) -> Engine:
        try:
            cls = self.data[cfg.host.scheme]
        except KeyError:
            logger.info(f"No registered engine provision for {cfg.host.scheme}")
            return NoopEngine(cfg)
        return cls(cfg)


_registry = _Registry()


def register_engine(schema: str):
    return _registry.add(schema)


def get_engine(cfg: Config) -> Engine:
    return _registry.get_(cfg)


class ProvisionError(DaggerError):
    """Error while provisioning the Dagger engine."""
