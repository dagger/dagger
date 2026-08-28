import dagger
from dagger import dag, enum_type, function, object_type


@enum_type
class Severity(dagger.Enum):
    """Vulnerability severity levels"""

    UNKNOWN = "UNKNOWN", "Undetermined risk; analyze further"
    LOW = "LOW", "Minimal risk; routine fix"
    MEDIUM = "MEDIUM", "Moderate risk; timely fix"
    HIGH = "HIGH", "Serious risk; quick fix needed."
    CRITICAL = "CRITICAL", "Severe risk; immediate action."


@object_type
class MyModule:
    @function
    def scan(self, ref: str, severity: Severity) -> str:
        ctr = dag.container().from_(ref)

        return (
            dag.container()
            .from_("aquasec/trivy:0.50.4")
            .with_mounted_file("/mnt/ctr.tar", ctr.as_tarball())
            .with_mounted_cache("/root/.cache", dag.cache_volume("trivy-cache"))
            .with_exec(
                [
                    "trivy",
                    "image",
                    "--format=json",
                    "--no-progress",
                    "--exit-code=1",
                    "--vuln-type=os,library",
                    "--severity=" + severity,
                    "--show-suppressed",
                    "--input=/mnt/ctr.tar",
                ]
            )
            .stdout()
        )
