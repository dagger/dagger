import dagger
from dagger.mod import function


@function
async def scan_image(
    image_ref: str,
    severity: str = "UNKNOWN,LOW,MEDIUM,HIGH,CRITICAL",
    exit_code: int = 0,
    format: str = "table",
) -> str:
    return await (
        dagger.container()
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
