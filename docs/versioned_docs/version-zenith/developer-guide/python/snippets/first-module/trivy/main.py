"""Security scanning with Trivy."""
from typing import Annotated

from dagger import Doc, dag, function, object_type


@object_type
class Trivy:
    """Functions for scanning images for vulnerabilities using Trivy"""

    @function
    async def scan_image(
        self,
        image_ref: Annotated[
            str,
            Doc("The image reference to scan"),
        ],
        severity: Annotated[
            str,
            Doc("Severity levels to scan for"),
        ] = "UNKNOWN,LOW,MEDIUM,HIGH,CRITICAL",
        exit_code: Annotated[
            int,
            Doc("The exit code to return if vulnerabilities are found"),
        ]= 0,
        format: Annotated[
            str,
            Doc("The output format to use for the scan results"),
        ] = "table",
    ) -> str:
        """Scan the specified image for vulnerabilities."""
        return await (
            dag.container()
            .from_("aquasec/trivy:latest")
            .with_exec(
                [
                    "image",
                    "--quiet",
                    "--severity",
                    severity,
                    "--exit-code",
                    str(exit_code),
                    "--format",
                    format,
                    image_ref,
                ]
            )
            .stdout()
        )
