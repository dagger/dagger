import dagger


def test_timeout():
    conn = dagger.Connection(dagger.Config(timeout=20))
    assert conn.connector.cfg.timeout == 20
