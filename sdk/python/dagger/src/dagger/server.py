import sys
import json
import logging
from typing import Any, NewType

import strawberry

FSID = strawberry.scalar(
    NewType('FSID', str),
    serialize=lambda v: v,
    parse_value=lambda v: v,
)

SecretID = strawberry.scalar(
    NewType('SecretID', str),
    serialize=lambda v: v,
    parse_value=lambda v: v,
)

Filesystem = NewType('Filesystem', object)
Exec = NewType('Exec', object)


@strawberry.type
class Filesystem:
    id: FSID
    exec: Exec
    dockerbuild: Filesystem
    file: str


@strawberry.type
class Exec:
    fs: Filesystem
    stdout: str
    stderr: str
    exitcode: int
    mount: Filesystem


logging.basicConfig(stream=sys.stdout, level=logging.DEBUG)


class Server:

    def __init__(self, module_name: str = '__main__'):
        self._module_name = module_name

    def _read_inputs(self) -> dict[str, Any]:
        with open('/inputs/dagger.json') as f:
            return json.loads(f.read())

    def _write_outputs(self, result: Any) -> None:
        with open('/outputs/dagger.json', 'w') as f:
            f.write(json.dumps(result))

    def _call_resolver(self, inputs: dict) -> Any:
        def check(x):
            if x not in inputs:
                raise RuntimeError('missing {}'.format(x))

        for i in ['args', 'parent', 'resolver']:
            check(i)

        split = inputs['resolver'].split('.', 2)
        typ, field = split[0], split[1]
        obj = getattr(sys.modules[self._module_name], typ)

        inst = None
        try:
            # if we have parent args, pass them to init the object
            inst = obj(**inputs['parent']) if inputs['parent'] else obj()
        except TypeError:
            # Sometimes we cannot init the main obj, likely because of named args:
            # the args passed should be used to init the sub-types
            # and the sub-tuypes instances should be used to init the main obj)
            # the end object should be jsonified and returned.
            # FIXME: This is a temporary hack, ideally the object should always be initianted
            return inputs['args']
        resolver = getattr(inst, field)

        if callable(resolver):
            return resolver(**inputs['args'])

        return resolver

    def run(self) -> None:
        inputs = self._read_inputs()
        logging.debug('inputs <- {}'.format(inputs))
        result = self._call_resolver(inputs)
        logging.debug('outputs -> {}'.format(inputs))
        self._write_outputs(result)
