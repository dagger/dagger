from dagger.client.base import Root


class Client(Root):
    """The root of the DAG."""


dag = Client()


__all__ = [
    "Client",
    "dag",
]
