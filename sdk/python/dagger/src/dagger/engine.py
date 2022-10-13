import os
import logging
import subprocess

from .client import Client


class Engine:

    def __init__(self,
                 port: int = 8080,
                 workdir: str | None = None,
                 configPath: str | None = None):
        if workdir is None:
            workdir = os.environ.get('DAGGER_WORKDIR', os.getcwd())
        if configPath is None:
            configPath = os.environ.get('DAGGER_CONFIG', './dagger.json')
        self._config = {
            'port': port,
            'workdir': workdir,
            'configPath': configPath,
        }
        self._process = None

    def _spawn(self):
        args = [
            'dagger', 'dev',
            '--workdir', self._config['workdir'],
            '--port', str(self._config['port']),
            '-p', self._config['configPath'],
        ]
        self._process = subprocess.Popen(args)

    def _fail_if_incorrect_dagger_binary(self) -> None:
        null = open(os.devnull, 'w')
        completedProcess = subprocess.run(["dagger", "dev", "--help"], stdout=null, stderr=null)
        if completedProcess.returncode != 0:
            logging.error("⚠️  Please ensure that dagger binary in $PATH is v0.3.0 or newer - a.k.a. Cloak")
            exit(127)

    def __enter__(self) -> Client:
        self._fail_if_incorrect_dagger_binary()
        self._spawn()
        # FIXME: do a simple gql request to make sure the server is ready
        return Client(host="localhost", port=self._config['port'])

    def __exit__(self, *_):
        assert self._process is not None
        self._process.terminate()
        # Gives 5 seconds for the process to terminate properly
        self._process.wait(timeout=3)
        if self._process.poll() is None:
            self._process.kill()
