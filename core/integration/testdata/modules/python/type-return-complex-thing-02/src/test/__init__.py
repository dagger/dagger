import dagger
from dagger import dag

@dagger.object_type
class ScanReport:
    contents: str = dagger.field()
    authors: list[str] = dagger.field()

@dagger.object_type
class ScanResult:
    containers: list[dagger.Container] = dagger.field(name="targets")
    report: ScanReport = dagger.field()

@dagger.object_type
class Test:
    @dagger.function
    def scan(self) -> ScanResult:
        return ScanResult(
            containers=[
                dag.container().from_("alpine:3.22.1").with_exec(["echo", "hello world"]),
            ],
            report=ScanReport(
                contents="hello world",
                authors=["foo", "bar"],
            ),
        )
