import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    async def services(self) -> dagger.Service:
        """Run two services which are dependent on each other"""
        svc_a = (
            dag.container()
            .from_("nginx")
            .with_exposed_port(80)
            .as_service(
                args=[
                    "sh",
                    "-c",
                    "nginx & while true; do curl svcb:80 && sleep 1; done",
                ]
            )
            .with_hostname("svca")
        )

        await svc_a.start()

        svc_b = (
            dag.container()
            .from_("nginx")
            .with_exposed_port(80)
            .as_service(
                args=[
                    "sh",
                    "-c",
                    "nginx & while true; do curl svca:80 && sleep 1; done",
                ]
            )
            .with_hostname("svcb")
        )

        await svc_b.start()

        return svc_b
