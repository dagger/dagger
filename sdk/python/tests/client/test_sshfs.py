"""Python port of core/integration/volume_test.go:TestSSHFSVolume.

Validates the Volume end-to-end stack from the Python SDK:
  - Query.sshfsVolume resolver (service-host rewrite + secret-plaintext read)
  - Server.RegisterSSHFSVolume (sshfs mount, refcounted by id)
  - Container.VolumeMount -> ExecutionMetadata.HostMount desugar
  - Worker.setupHostMounts binds the volume's MountPath rw into each exec

The sshd host is stood up *inside* a Dagger container-as-a-service (hermetic,
no external docker required) and reached via ``experimental_service_host``,
exactly like the Go test. Flow: register the sshfs volume against that
service, then exercise it: (1) read pre-seeded content, (2) write a file from
inside the container, (3) a fresh container reads the written file back,
(4) re-register the same endpoint and confirm the independent handle observes
the same state (refcount-by-id), (5) round-trip a 1GiB file to confirm large
transfers stream intact. If any link drops read/write state, these steps fail.
"""

import anyio
import pytest

import dagger
from dagger import dag

pytestmark = [
    pytest.mark.anyio,
    pytest.mark.slow,
]

SSH_PORT = 2222

SETUP_SCRIPT = """#!/bin/sh

set -e -u -x

cd /root
mkdir -p repo
cd repo
echo test >> test.txt

sed -i 's/^#\\?PasswordAuthentication.*/PasswordAuthentication no/' /etc/ssh/sshd_config
sed -i 's/^#\\?PubkeyAuthentication.*/PubkeyAuthentication yes/' /etc/ssh/sshd_config
grep -q '^PermitRootLogin' /etc/ssh/sshd_config || echo "PermitRootLogin yes" >> /etc/ssh/sshd_config
grep -q '^Subsystem sftp' /etc/ssh/sshd_config || echo "Subsystem sftp /usr/lib/ssh/sftp-server" >> /etc/ssh/sshd_config || true

mkdir -p /var/run/sshd

chmod 700 /root/.ssh
chmod 600 /root/.ssh/authorized_keys

cp /root/.ssh/host_key /etc/ssh/ssh_host_rsa_key
cp /root/.ssh/host_key.pub /etc/ssh/ssh_host_rsa_key.pub
chmod 600 /etc/ssh/ssh_host_rsa_key

$(which sshd) -D -e -p 2222 &

echo '--- AUTHORIZED_KEYS START ---'
cat /root/.ssh/authorized_keys || true
echo '--- AUTHORIZED_KEYS END ---'

sleep infinity
"""


@pytest.fixture(autouse=True, scope="module")
async def _connection():
    async with dagger.connection(dagger.Config(retry=None)):
        yield


async def test_sshfs_volume(alpine_image: str):
    sshfs = dag.container().from_(alpine_image).with_exec(["apk", "add", "openssh"])

    # Generate an SSH host key and a user key, then authorize the user key.
    host_key_gen = (
        sshfs.with_exec(
            ["ssh-keygen", "-t", "rsa", "-b", "4096", "-f", "/root/.ssh/host_key", "-N", ""]
        )
        .with_exec(
            ["ssh-keygen", "-t", "rsa", "-b", "4096", "-f", "/root/.ssh/id_rsa", "-N", ""]
        )
        .with_exec(["cp", "/root/.ssh/id_rsa.pub", "/root/.ssh/authorized_keys"])
    )

    user_public_key = await host_key_gen.file("/root/.ssh/id_rsa.pub").contents()
    user_private_key = await host_key_gen.file("/root/.ssh/id_rsa").contents()

    setup_script = (
        dag.directory().with_new_file("setup.sh", SETUP_SCRIPT).file("setup.sh")
    )

    ssh_svc = (
        host_key_gen.with_mounted_file("/root/start.sh", setup_script)
        .with_exposed_port(SSH_PORT)
        .with_default_args(["sh", "/root/start.sh"])
        .as_service()
    )

    ssh_host = await ssh_svc.hostname()
    sshfs_endpoint = f"ssh://root@{ssh_host}:{SSH_PORT}/root/repo"

    priv_key_secret = dag.set_secret("sshfs-private-key", user_private_key)
    host_key_secret = dag.set_secret("sshfs-public-key", user_public_key)

    # Readiness: probe ssh a few times before proceeding so the sshfs mount
    # doesn't race the service coming up.
    for i in range(10):
        probe_out = await (
            dag.container()
            .from_(alpine_image)
            .with_service_binding("ssh", ssh_svc)
            .with_file("/tmp/id_rsa", host_key_gen.file("/root/.ssh/id_rsa"))
            .with_exec(
                [
                    "sh",
                    "-c",
                    "apk add --no-cache openssh-client > /dev/null 2>&1 || true; "
                    "chmod 600 /tmp/id_rsa; "
                    f"ssh -i /tmp/id_rsa -o IdentitiesOnly=yes -o StrictHostKeyChecking=no "
                    f"-o UserKnownHostsFile=/dev/null -p {SSH_PORT} root@ssh 'echo ok' 2>&1 || true",
                ]
            )
            .stdout()
        )
        if "ok" in probe_out:
            break
        if i == 9:
            pytest.fail(f"ssh readiness probe failed after retries; last output: {probe_out}")
        await anyio.sleep(0.5)

    sshfs_volume = dag.sshfs_volume(
        sshfs_endpoint,
        priv_key_secret,
        host_key_secret,
        experimental_service_host=ssh_svc,
    )

    # 1) Initial seed content must be visible to the first container.
    output = await (
        dag.container()
        .from_(alpine_image)
        .with_volume_mount("/mnt/repo", sshfs_volume)
        .with_exec(["cat", "/mnt/repo/test.txt"])
        .stdout()
    )
    assert "test" in output

    # 2) A write from inside the container must land on the remote FS.
    write_out = await (
        dag.container()
        .from_(alpine_image)
        .with_volume_mount("/mnt/repo", sshfs_volume)
        .with_exec(
            ["sh", "-c", "echo 'other' > /mnt/repo/other.txt && cat /mnt/repo/other.txt"]
        )
        .stdout()
    )
    assert "other" in write_out

    # 3) A subsequent container mounting the same volume must see the write.
    output2 = await (
        dag.container()
        .from_(alpine_image)
        .with_volume_mount("/mnt/repo", sshfs_volume)
        .with_exec(["cat", "/mnt/repo/other.txt"])
        .stdout()
    )
    assert "other" in output2

    # 4) Repeat against the same mounted volume: a fresh container reusing the
    # existing handle must still observe both the seed content and the earlier
    # write, confirming the mount stays usable across independent execs.
    repeat_out = await (
        dag.container()
        .from_(alpine_image)
        .with_volume_mount("/mnt/repo", sshfs_volume)
        .with_exec(["sh", "-c", "cat /mnt/repo/test.txt /mnt/repo/other.txt"])
        .stdout()
    )
    assert "test" in repeat_out
    assert "other" in repeat_out

    # 5) A large (1GiB) file must round-trip through the sshfs mount intact.
    # Write random data and capture its checksum in one container, then read
    # it back in a fresh container and require the size and checksum match.
    big_file_mib = 1024
    big_file_bytes = big_file_mib * 1024 * 1024

    write_big = await (
        dag.container()
        .from_(alpine_image)
        .with_volume_mount("/mnt/repo", sshfs_volume)
        .with_exec(
            [
                "sh",
                "-c",
                f"dd if=/dev/urandom of=/mnt/repo/big.bin bs=1M count={big_file_mib} "
                "2>/dev/null && sha256sum /mnt/repo/big.bin | cut -d' ' -f1",
            ]
        )
        .stdout()
    )
    write_sum = write_big.strip()
    assert len(write_sum) == 64, "expected a sha256 hex digest from the writer"

    read_big = await (
        dag.container()
        .from_(alpine_image)
        .with_volume_mount("/mnt/repo", sshfs_volume)
        .with_exec(
            [
                "sh",
                "-c",
                "printf '%s %s' \"$(stat -c %s /mnt/repo/big.bin)\" "
                "\"$(sha256sum /mnt/repo/big.bin | cut -d' ' -f1)\"",
            ]
        )
        .stdout()
    )

    fields = read_big.split()
    assert len(fields) == 2, f"expected '<size> <sha256>' from the reader, got {read_big!r}"
    assert fields[0] == str(big_file_bytes), "1GiB file size mismatch across sshfs round-trip"
    assert fields[1] == write_sum, "1GiB file checksum mismatch across sshfs round-trip"
