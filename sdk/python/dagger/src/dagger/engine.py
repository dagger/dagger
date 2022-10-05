import os
import subprocess

from .client import Client


class Engine:

    def __init__(self,
                 port: int = 8080,
                 workdir: str = None,
                 configPath: str = None):
        if workdir is None:
            workdir = os.environ.get('DAGGER_WORKDIR', os.getcwd())
        if configPath is None:
            configPath = os.environ.get('DAGGER_CONFIG', './dagger.yaml')
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

    def __enter__(self) -> Client:
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
