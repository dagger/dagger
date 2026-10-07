"""Format identifiers from the words the engine parsed them into.

The engine parses every schema name into words and sends them along with the
introspection result, under the ``__identifiers`` key. Formatting those words
here, instead of guessing at word boundaries, keeps acronyms and dictionary
terms intact (``prerequisiteSHAs`` becomes ``prerequisite_shas``, not
``prerequisite_sh_as``).

The rules mirror ``engine/naming`` and are checked against its shared test
vectors.
"""

import enum
from collections.abc import Iterable, Mapping, Sequence
from dataclasses import dataclass
from typing import Any, TypeAlias


class Casing(enum.Enum):
    """A convention for joining words into an identifier."""

    PASCAL = "PASCAL"
    """Every word capitalized: ``HTTPClient``."""

    CAMEL = "CAMEL"
    """First word lowercase, the rest capitalized: ``httpClient``."""

    SNAKE = "SNAKE"
    """Lowercase, joined with ``_``: ``http_client``."""

    SCREAMING_SNAKE = "SCREAMING_SNAKE"
    """Uppercase, joined with ``_``: ``HTTP_CLIENT``."""


class AcronymStyle(enum.Enum):
    """How acronyms and terms are written where a word starts with a capital.

    Only affects :attr:`Casing.PASCAL` and :attr:`Casing.CAMEL`.
    """

    UPPERCASE = "UPPERCASE"
    """``HTTPClient``, ``IPv6Address``, ``GitHubRepo``."""

    CAPITALIZED = "CAPITALIZED"
    """``HttpClient``, ``Ipv6Address``, ``GitHubRepo``."""


class WordKind(enum.Enum):
    WORD = "WORD"
    """An ordinary word."""

    ACRONYM = "ACRONYM"
    """An acronym, from the dictionary or a run of capitals."""

    TERM = "TERM"
    """A dictionary term with a fixed mixed-case spelling (GitHub, IPv6)."""


def _upper_first(s: str) -> str:
    return s[:1].upper() + s[1:]


@dataclass(frozen=True, slots=True)
class Word:
    """One word of a parsed identifier."""

    kind: WordKind
    text: str
    """The word's spelling, without its suffix."""

    suffix: str = ""
    """A plural ``s`` or digits glued to the word."""

    capitalized: str = ""
    """The word's capitalized form, without its suffix (``Http``, ``GitHub``)."""

    @classmethod
    def from_json(cls, data: Mapping[str, Any]) -> "Word":
        text = data["text"]
        return cls(
            kind=WordKind(data.get("kind") or "WORD"),
            text=text,
            suffix=data.get("suffix") or "",
            capitalized=data.get("capitalized") or _upper_first(text.lower()),
        )

    def lower(self) -> str:
        return (self.text + self.suffix).lower()

    def upper(self) -> str:
        return (self.text + self.suffix).upper()

    def title(self, style: AcronymStyle) -> str:
        """The word's form where it starts with a capital, suffix included."""
        if self.kind is WordKind.WORD or style is AcronymStyle.CAPITALIZED:
            text = self.capitalized or _upper_first(self.text.lower())
        else:
            text = _upper_first(self.text)
        return text + self.suffix


Words: TypeAlias = Sequence[Word]
Identifiers: TypeAlias = Mapping[str, Words]


def format_words(
    words: Iterable[Word],
    casing: Casing,
    style: AcronymStyle = AcronymStyle.UPPERCASE,
) -> str:
    """Format an identifier's words in a casing."""
    words = list(words)
    match casing:
        case Casing.PASCAL:
            return "".join(w.title(style) for w in words)
        case Casing.CAMEL:
            if not words:
                return ""
            first, *rest = words
            return first.lower() + "".join(w.title(style) for w in rest)
        case Casing.SNAKE:
            return "_".join(w.lower() for w in words)
        case Casing.SCREAMING_SNAKE:
            return "_".join(w.upper() for w in words)
    msg = f"unknown casing: {casing!r}"
    raise ValueError(msg)


def parse_identifiers(data: Mapping[str, Any] | None) -> dict[str, list[Word]] | None:
    """Read the ``__identifiers`` object of an introspection result."""
    if data is None:
        return None
    return {
        name: [Word.from_json(w) for w in words]
        for name, words in data.items()
        if words
    }
