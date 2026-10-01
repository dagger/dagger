"""A module for HelloWithServicesPy functions"""

import dagger
from dagger import dag, function, object_type, up


@object_type
class Infra:
    @function
    @up
    def database(self) -> dagger.Service:
        """Returns a postgres database service"""
        return (
            dag.container()
            .from_("postgres:alpine")
            .with_env_variable("POSTGRES_PASSWORD", "test")
            .with_exposed_port(5432)
            .as_service()
        )


@object_type
class HelloWithServicesPy:
    @function
    @up
    def web(self) -> dagger.Service:
        """Returns a web server service"""
        return (
            dag.container()
            .from_("nginx:alpine")
            .with_exposed_port(80)
            .as_service()
        )

    @function
    @up
    def redis(self) -> dagger.Service:
        """Returns a redis service"""
        return (
            dag.container()
            .from_("redis:alpine")
            .with_exposed_port(6379)
            .as_service()
        )

    @function
    def infra(self) -> Infra:
        return Infra()
