import json
import os
import pathlib

import pytest

from codegen.naming import (
    AcronymStyle,
    Casing,
    Word,
    WordKind,
    format_words,
    parse_identifiers,
)


def _find_vectors() -> pathlib.Path | None:
    """Locate the engine's shared test vectors.

    CI mounts them at $DAGGER_NAMING_VECTORS; in a checkout, they're found
    up from this file.
    """
    if env := os.environ.get("DAGGER_NAMING_VECTORS"):
        return pathlib.Path(env)
    for parent in pathlib.Path(__file__).resolve().parents:
        path = parent / "engine" / "naming" / "testdata" / "vectors.json"
        if path.is_file():
            return path
    return None


VECTORS_PATH = _find_vectors()

VECTORS = json.loads(VECTORS_PATH.read_text()) if VECTORS_PATH else []

FORMATS = {
    "PASCAL": (Casing.PASCAL, AcronymStyle.UPPERCASE),
    "PASCAL_CAPITALIZED": (Casing.PASCAL, AcronymStyle.CAPITALIZED),
    "CAMEL": (Casing.CAMEL, AcronymStyle.UPPERCASE),
    "CAMEL_CAPITALIZED": (Casing.CAMEL, AcronymStyle.CAPITALIZED),
    "SNAKE": (Casing.SNAKE, AcronymStyle.UPPERCASE),
    "SCREAMING_SNAKE": (Casing.SCREAMING_SNAKE, AcronymStyle.UPPERCASE),
}


def test_vectors_found():
    assert VECTORS_PATH is not None, "engine/naming/testdata/vectors.json not found"
    assert VECTORS


@pytest.mark.parametrize(
    "vector",
    [v for v in VECTORS if "words" in v],
    ids=lambda v: v["input"],
)
@pytest.mark.parametrize("fmt", FORMATS)
def test_vectors(vector, fmt):
    casing, style = FORMATS[fmt]
    words = [Word.from_json(w) for w in vector["words"]]
    assert format_words(words, casing, style) == vector["formats"][fmt]


@pytest.mark.parametrize(
    ("words", "casing", "style", "expected"),
    [
        (
            [Word(WordKind.TERM, "iOS", capitalized="Ios"), Word(WordKind.WORD, "app")],
            Casing.PASCAL,
            AcronymStyle.UPPERCASE,
            "IOSApp",
        ),
        (
            [Word(WordKind.WORD, "list"), Word(WordKind.ACRONYM, "PR", "s", "Pr")],
            Casing.CAMEL,
            AcronymStyle.UPPERCASE,
            "listPRs",
        ),
        (
            [Word(WordKind.WORD, "list"), Word(WordKind.ACRONYM, "PR", "s", "Pr")],
            Casing.SCREAMING_SNAKE,
            AcronymStyle.UPPERCASE,
            "LIST_PRS",
        ),
        ([], Casing.CAMEL, AcronymStyle.UPPERCASE, ""),
    ],
)
def test_format_words(words, casing, style, expected):
    assert format_words(words, casing, style) == expected


def test_parse_identifiers():
    http = {"kind": "ACRONYM", "text": "HTTP", "suffix": "", "capitalized": "Http"}
    client = {"kind": "WORD", "text": "client", "suffix": "", "capitalized": "Client"}
    assert parse_identifiers(None) is None
    assert parse_identifiers({"httpClient": [http, client]}) == {
        "httpClient": [
            Word(WordKind.ACRONYM, "HTTP", "", "Http"),
            Word(WordKind.WORD, "client", "", "Client"),
        ]
    }


def test_word_from_json_defaults():
    # Missing optional keys fall back to the word's own spelling.
    assert Word.from_json({"kind": "ACRONYM", "text": "HTTPX"}) == Word(
        WordKind.ACRONYM, "HTTPX", "", "Httpx"
    )
