import pytest

from dagger import ClientConnectionError
from dagger.client._session import ConnectParams


def test_independent_session_env_provisions_cli_without_inherited_token(
    monkeypatch: pytest.MonkeyPatch,
):
    monkeypatch.setenv("DAGGER_NESTING", "INDEPENDENT_SESSIONS")
    monkeypatch.setenv("DAGGER_SESSION_PORT", "1234")
    monkeypatch.delenv("DAGGER_SESSION_TOKEN", raising=False)

    assert ConnectParams.from_env() is None


@pytest.mark.parametrize("nesting", ["NESTED_CLIENT", "INDEPENDENT_SESSIONS"])
def test_explicit_nesting_requires_port(monkeypatch: pytest.MonkeyPatch, nesting: str):
    monkeypatch.setenv("DAGGER_NESTING", nesting)
    monkeypatch.delenv("DAGGER_SESSION_PORT", raising=False)

    with pytest.raises(ClientConnectionError, match="requires DAGGER_SESSION_PORT"):
        ConnectParams.from_env()


def test_unknown_nesting_is_rejected(monkeypatch: pytest.MonkeyPatch):
    monkeypatch.setenv("DAGGER_NESTING", "UNKNOWN")

    with pytest.raises(ClientConnectionError, match="Unknown DAGGER_NESTING"):
        ConnectParams.from_env()
