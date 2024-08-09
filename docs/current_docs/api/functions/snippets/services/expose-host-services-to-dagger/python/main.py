from typing import Annotated

import dagger
from dagger import Doc, dag, function, object_type


@object_type
class MyModule:
    @function
    async def user_list(
        self, svc: Annotated[dagger.Service, Doc("Host service")]
    ) -> str:
        """Send a query to a MariaDB service and return the response."""
        return await (
            dag.container()
            .from_("mariadb:10.11.2")
            .with_service_binding("db", svc)
            .with_exec(
                [
                    "/usr/bin/mysql",
                    "--user=root",
                    "--password=secret",
                    "--host=db",
                    "-e",
                    "SELECT Host, User FROM mysql.user",
                ]
            )
            .stdout()
        )
