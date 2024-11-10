from dagger.client._session import SharedConnection

_shared = SharedConnection()
connect = _shared.connect
close = _shared.close
