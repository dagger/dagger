import sys

import httpx
import pytest
from pytest_subprocess.fake_process import FakeProcess

import dagger
from dagger._engine import session


def test_getting_connect_params(fp: FakeProcess):
    fp.register(
        ["dagger", "session", fp.any()],
        stdout=['{"port":50004,"session_token":"abc"}', ""],
    )
    with session.start_cli_session_sync(dagger.Config(), "dagger") as conn:
        assert conn.url == httpx.URL("http://127.0.0.1:50004/query")
        assert conn.port == 50004
        assert conn.session_token == "abc"


@pytest.mark.parametrize("config_args", [{"log_output": sys.stderr}, {}])
@pytest.mark.parametrize(
    "call_kwargs",
    [
        {"stderr": ["Error: buildkit failed to respond", ""], "returncode": 1},
        {
            "stderr": ['Error: unknown command "session" for "dagger"', ""],
            "returncode": 1,
        },
        {"stdout": []},
        {"stdout": ['{"port":50004}', ""]},
        {"stdout": ['{"port":"abc","session_token":"abc"}', ""]},
        {"stdout": ['{"port":"","session_token":"abc"}', ""]},
        {"stdout": ['{"port":0,"session_token":"abc"}', ""]},
        {"stdout": ['{"session_token":"abc"}', ""]},
        {"stdout": ["dagger devel", ""]},
        {"stdout": ["50004", ""]},
    ],
)
def test_cli_exec_errors(config_args: dict, call_kwargs: dict, fp: FakeProcess):
    fp.register(
        ["dagger", "session", fp.any()],
        **call_kwargs,
    )
    with (
        pytest.raises(
            dagger.ProvisionError,
            match="Failed to start Dagger engine session",
        ),
        session.start_cli_session_sync(
            dagger.Config(**config_args),
            "dagger",
        ),
    ):
        ...


def test_stderr(fp: FakeProcess):
    fp.register(
        ["dagger", "session", fp.any()],
        stderr=["Error: buildkit failed to respond", ""],
        returncode=1,
    )
    with (
        pytest.raises(
            dagger.ProvisionError,
            match="buildkit failed to respond",
        ),
        session.start_cli_session_sync(
            dagger.Config(),
            "dagger",
        ),
    ):
        ...
