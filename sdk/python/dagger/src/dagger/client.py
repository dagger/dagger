from typing import Any

from gql.transport import requests
from gql import Client as gqlClient, gql


class Client:

    def __init__(self, host: str, port: int):
        self._client = None
        transport = requests.RequestsHTTPTransport(
            url="http://{}:{}/".format(host, port),
            timeout=30,
            retries=10,
        )
        self._client = gqlClient(transport=transport,
                                 fetch_schema_from_transport=True)

    def do(self, query: str) -> dict[str, Any]:
        return self._client.execute(gql(query))
