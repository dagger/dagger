import sys

import httpx
import pytest
from pytest_subprocess.fake_process import FakeProcess

import dagger
from dagger.engine import cli
from dagger.exceptions import ProvisionError


def test_getting_connect_params(fp: FakeProcess):
    fp.register(
        ["dagger", "session"],
        stdout=['{"port":50004,"session_token":"abc"}', ""],
    )
    with cli.CLISession(dagger.Config(), "dagger") as conn:
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
        ["dagger", "session"],
        **call_kwargs,
    )
    with pytest.raises(ProvisionError) as exc_info:
        with cli.CLISession(dagger.Config(**config_args), "dagger"):
            ...

    assert "Dagger engine failed to start" in str(exc_info.value)
