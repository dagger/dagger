from dataclasses import dataclass, field

from rich.console import Console
from rich.status import Status
from typing_extensions import Self


@dataclass(slots=True)
class Progress:
    console: Console
    status: Status | None = field(default=None, init=False)

    def start(self, status: str) -> None:
        self.status = Status(status, console=self.console)
        self.status.start()

    def stop(self) -> None:
        if self.status:
            self.status.stop()
            self.status = None

    async def __aenter__(self) -> Self:
        return self

    async def __aexit__(self, *_) -> None:
        self.stop()

    def update(self, message: str) -> None:
        if self.status:
            self.status.update(message)
