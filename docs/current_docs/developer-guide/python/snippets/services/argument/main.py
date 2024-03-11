import dagger
from dagger import dag, function, object_type

@object_type
class MyModule:
    @function
    async def user_list(self, host_service: dagger.Service) -> str:
        """Sends a query to a MariaDB service received as input and returns the response"""
        return await (
            dag.container()
            .from_("mariadb:10.11.2")
            .with_service_binding("db", host_service)
            .with_exec(["/bin/sh", "-c", "/usr/bin/mysql --user=root --password=secret --host=db -e 'SELECT Host, User FROM mysql.user'"])
            .stdout()
        )
