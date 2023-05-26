import sys
import os
import anyio
import dagger

async def main():

    # check for Docker Hub registry credentials in host environment
    for var in ["DOCKERHUB_USERNAME", "DOCKERHUB_PASSWORD"]:
        if var not in os.environ:
            raise EnvironmentError('"%s" environment variable must be set' % var)

    # initialize Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:

        username = os.environ.get("DOCKERHUB_USERNAME");
        # set registry password as secret for Dagger pipeline
        password = client.set_secret("password", os.environ.get("DOCKERHUB_PASSWORD"))

        # create a cache volume for Maven downloads
        maven_cache = client.cache_volume('maven-cache');

        # get reference to source code directory
        source = client.host().directory(".", exclude=["ci", ".venv"])

        # create database service container
        mariadb = (
          client.container()
          .from_('mariadb:10.11.2')
          .with_env_variable('MARIADB_USER', 'petclinic')
          .with_env_variable('MARIADB_PASSWORD', 'petclinic')
          .with_env_variable('MARIADB_DATABASE', 'petclinic')
          .with_env_variable('MARIADB_ROOT_PASSWORD', 'root')
          .with_exposed_port(3306)
          .with_exec([])
        )

        # use maven:3.9 container
        # mount cache and source code volumes
        # set working directory
        app = (
          client.container()
          .from_('maven:3.9-eclipse-temurin-17')
          .with_mounted_cache('/root/.m2', maven_cache)
          .with_mounted_directory('/app', source)
          .with_workdir('/app')
        )

        # define binding between
        # application and service containers
        # define JDBC URL for tests
        # test, build and package application as JAR
        build = (
          app.with_service_binding('db', mariadb)
          .with_env_variable('MYSQL_URL', 'jdbc:mysql://petclinic:petclinic@db/petclinic')
          .with_exec(['mvn', '-Dspring.profiles.active=mysql', 'clean', 'package'])
        )

        # use eclipse alpine container as base
        # copy JAR files from builder
        # set entrypoint and database profile
        deploy = (
          client.container()
          .from_('eclipse-temurin:17-alpine')
          .with_directory('/app', build.directory('./target'))
          .with_entrypoint(['java', '-jar', '-Dspring.profiles.active=mysql', '/app/spring-petclinic-3.0.0-SNAPSHOT.jar'])
        )

        # publish image to registry
        address = await (
          deploy.with_registry_auth('docker.io', username, password)
          .publish(f"{username}/myapp")
        )

        # print image address
        print(f"Image published at: {address}")

anyio.run(main)
