import contextlib
import io
import os
from unittest.mock import AsyncMock

import httpx
import pytest
from exceptiongroup import ExceptionGroup
from pytest_httpx import HTTPXMock

import dagger
from dagger.provisioning import _engine as engine
from dagger.provisioning._download import Downloader
from dagger.provisioning._exceptions import (
    CLIReleaseUnavailableError,
    DownloadError,
    SessionError,
)


def test_fallback_to_local_cli(
    tmp_path,
    monkeypatch: pytest.MonkeyPatch,
):
    bin_name = "dagger.exe" if os.name == "nt" else "dagger"
    bin_path = tmp_path / bin_name
    bin_path.touch(mode=0o700)
    monkeypatch.setenv("PATH", str(tmp_path))

    download_error = CLIReleaseUnavailableError("download failed")
    logs = io.StringIO()

    actual = engine.fallback_to_local_cli(download_error, logs)

    assert actual == str(bin_path)
    assert logs.getvalue() == (
        f"CLI version {engine.CLI_VERSION} is unavailable; using {bin_path} from PATH "
        "(version compatibility is not guaranteed).\n"
    )


def test_no_fallback_to_local_cli_for_other_errors():
    download_error = DownloadError("download failed")

    with pytest.raises(DownloadError) as exc_info:
        engine.fallback_to_local_cli(download_error)

    assert exc_info.value is download_error


def test_fallback_preserves_download_and_path_errors(
    tmp_path,
    monkeypatch: pytest.MonkeyPatch,
):
    monkeypatch.setenv("PATH", str(tmp_path))
    download_error = CLIReleaseUnavailableError("download failed")

    with pytest.raises(ExceptionGroup) as exc_info:
        engine.fallback_to_local_cli(download_error)

    assert exc_info.value.exceptions[0] is download_error
    assert isinstance(exc_info.value.exceptions[1], FileNotFoundError)
    assert exc_info.value.__cause__ is exc_info.value.exceptions[1]
    assert "Failed to download the Dagger CLI" in str(exc_info.value)
    assert "dagger executable was not found" in str(exc_info.value)


@pytest.mark.parametrize("status_code", [403, 404])
def test_checksum_marks_release_unavailable(
    status_code: int,
    httpx_mock: HTTPXMock,
):
    downloader = Downloader(version="unreleased")
    httpx_mock.add_response(url=downloader.checksum_url, status_code=status_code)

    with pytest.raises(CLIReleaseUnavailableError):
        downloader.expected_checksum()


@pytest.mark.parametrize("status_code", [403, 404])
def test_missing_archive_does_not_mark_release_unavailable(
    status_code: int,
    httpx_mock: HTTPXMock,
):
    # Missing checksums mean the release is absent; a missing archive may be a
    # partial or broken release, so it must remain fatal.
    downloader = Downloader(version="unreleased")
    httpx_mock.add_response(url=downloader.archive_url, status_code=status_code)

    with pytest.raises(httpx.HTTPStatusError) as exc_info:
        downloader.extract_cli_archive(io.BytesIO())

    assert not isinstance(exc_info.value, CLIReleaseUnavailableError)


@pytest.mark.anyio
async def test_fallback_session_error_preserves_download_error(
    tmp_path,
    monkeypatch: pytest.MonkeyPatch,
):
    bin_name = "dagger.exe" if os.name == "nt" else "dagger"
    bin_path = tmp_path / bin_name
    bin_path.touch(mode=0o700)
    monkeypatch.setenv("PATH", str(tmp_path))

    download_error = CLIReleaseUnavailableError("download failed")
    session_error = SessionError("session failed")
    stack = contextlib.AsyncExitStack()
    instance = engine.Engine(dagger.Config(log_output=io.StringIO()), stack)
    instance.progress = AsyncMock()
    monkeypatch.setattr(instance, "get_cli", AsyncMock(side_effect=download_error))

    @contextlib.asynccontextmanager
    async def fail_to_start_cli_session(*_):
        raise session_error
        yield

    monkeypatch.setattr(engine, "start_cli_session", fail_to_start_cli_session)

    with pytest.raises(ExceptionGroup) as exc_info:
        await instance.provision()

    assert exc_info.value.exceptions == (download_error, session_error)
    assert exc_info.value.__cause__ is session_error
    assert "Failed to download the Dagger CLI" in str(exc_info.value)
    assert "Failed to start Dagger engine session" in str(exc_info.value)
