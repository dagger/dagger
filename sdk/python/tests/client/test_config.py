import dagger


def test_compat_connect_timeout():
    cfg = dagger.Config(timeout=10)  # type: ignore reportGeneralTypeIssues
    assert cfg.timeout is not None
    assert cfg.timeout.connect == 10


def test_compat_execute_timeout():
    cfg = dagger.Config(execute_timeout=0.5)
    assert cfg.timeout is not None
    assert cfg.timeout.read == 0.5
    assert cfg.timeout.write == 0.5


def test_compat_connect_and_execute_timeout():
    cfg = dagger.Config(
        timeout=20,  # type: ignore reportGeneralTypeIssues
        execute_timeout=1,
    )
    assert cfg.timeout is not None
    assert cfg.timeout.connect == 20
    assert cfg.timeout.read == 1
    assert cfg.timeout.write == 1


def test_connect_and_execute_timeout():
    cfg = dagger.Config(timeout=dagger.Timeout(3600, connect=15))
    assert cfg.timeout is not None
    assert cfg.timeout.connect == 15
    assert cfg.timeout.read == 3600
    assert cfg.timeout.write == 3600
