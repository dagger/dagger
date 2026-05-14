"""conftest for the AST analyzer / mod tests.

Registers Hypothesis profiles before pytest's command-line
``--hypothesis-profile`` flag is processed. Profiles defined inside a
test module aren't visible at that point.
"""

from hypothesis import HealthCheck, settings

# CI default — fast.
settings.register_profile(
    "default",
    max_examples=50,
    deadline=None,
    suppress_health_check=[HealthCheck.too_slow],
)
# For chasing real bugs / before merging.
settings.register_profile(
    "thorough",
    max_examples=500,
    deadline=None,
    suppress_health_check=[HealthCheck.too_slow],
)
settings.load_profile("default")
