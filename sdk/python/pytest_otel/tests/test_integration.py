"""Integration tests to verify the plugin works end-to-end.

Run these tests with OTEL environment variables set to see spans exported.
Example:
    OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 \
    TRACEPARENT=00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01 \
    pytest tests/test_integration.py -v
"""

import logging


def test_simple_pass():
    """A simple passing test."""
    assert 1 + 1 == 2


def test_simple_fail_expected():
    """This test is expected to pass (despite its name)."""
    assert True


def test_with_logging():
    """Test that logs are captured."""
    logger = logging.getLogger(__name__)
    logger.info("This is an info message from the test")
    logger.warning("This is a warning message")
    assert True


class TestClass:
    """Test class to verify class-based test hierarchy."""

    def test_in_class(self):
        """Test method inside a class."""
        assert "hello".upper() == "HELLO"

    def test_another_in_class(self):
        """Another test method."""
        assert [1, 2, 3][-1] == 3


def test_with_exception_handling():
    """Test that handles an exception internally."""
    try:
        raise ValueError("Expected error")
    except ValueError:
        pass
    assert True


# Uncomment to test failure handling:
# def test_failure():
#     """This test will fail."""
#     assert False, "This failure should be recorded in the span"
