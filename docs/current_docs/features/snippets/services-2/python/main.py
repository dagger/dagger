<<<<<<< HEAD
import dagger
from dagger import dag, function, object_type
=======
from typing import Annotated

import dagger
from dagger import Doc, dag, function, object_type
>>>>>>> 81388975a (Added code snippets to feature pages)


@object_type
class MyModule:
    @function
<<<<<<< HEAD
    async def user_list(self, svc: dagger.Service) -> str:
=======
    async def user_list(
        self, svc: dagger.Service
    ) -> str:
>>>>>>> 81388975a (Added code snippets to feature pages)
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
