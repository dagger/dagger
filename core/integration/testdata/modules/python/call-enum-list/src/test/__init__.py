from typing import Final
import enum

import dagger
from dagger import dag


@dagger.enum_type
class Language(enum.Enum):
    GO = "GO"
    PYTHON = "PYTHON"
    TYPESCRIPT = "TYPESCRIPT"
    PHP = "PHP"
    ELIXIR = "ELIXIR"


FAVES: Final[Language] = [Language.GO, Language.PYTHON]


@dagger.object_type
class Test:
    @dagger.function
    def faves(self, langs: list[Language] = FAVES) -> str:
        return " ".join(lang.value for lang in langs)

    @dagger.function
    def official(self) -> list[Language]:
        return [Language.GO, Language.PYTHON, Language.TYPESCRIPT]
