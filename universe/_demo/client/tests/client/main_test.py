import http.client
import pytest

from src.client.main import get_response_msg

def test_get_response_msg():
    assert get_response_msg("hey there", 200) == "Server says: hey there"

def test_get_response_msg_failure():
    pytest.raises(RuntimeError, get_response_msg, "uh oh", 500)
