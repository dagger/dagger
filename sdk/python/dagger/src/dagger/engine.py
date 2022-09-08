import os
import subprocess
import multiprocessing

from .client import Client


class Engine:

    def __init__(self,
                 port: int = 8080,
                 workdir: str = None,
                 configPath: str = None):
        if workdir is None:
            workdir = os.environ.get('CLOAK_WORKDIR', os.getcwd())
        if configPath is None:
            configPath = os.environ.get('CLOAK_CONFIG', './cloak.yaml')
        self._config = {
            'port': port,
            'workdir': workdir,
            'configPath': configPath,
        }
        self._process = multiprocessing.Process(target=self._spawn)

    def _spawn(self):
        args = [
            'cloak', 'dev',
            '--workdir', self._config['workdir'],
            '--port', str(self._config['port']),
            '-p', self._config['configPath'],
        ]
        result = subprocess.run(args)
        result.check_returncode()

    def __enter__(self) -> Client:
        self._process.start()
        # FIXME: do a simple gql request to make sure the server is ready
        return Client(host="localhost", port=self._config['port'])

    def __exit__(self, exc_type, exc_value, exc_traceback):
        self._process.terminate()
        # Gives 5 seconds for the process to terminate properly
        self._process.join(timeout=5)
        if self._process.is_alive():
            self._process.kill()
            self._process.join()
