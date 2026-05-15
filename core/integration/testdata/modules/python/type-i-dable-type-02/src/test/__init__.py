from typing import Self

import dagger

@dagger.object_type
class Test:
    data: str = ""

    @dagger.function
    def set(self, data: str) -> Self:
        self.data = data
        return self

    @dagger.function
    def get(self) -> str:
        return self.data
