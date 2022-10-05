import dataclasses
import sys
import json
import logging
from collections.abc import Sequence
from typing import Any

# FIXME: we should have a custom logger instead of using the global one
logging.basicConfig(stream=sys.stdout, level=logging.DEBUG)


class Server:

    def __init__(self, module_name: str = '__main__'):
        self._module_name = module_name

    def _read_inputs(self) -> dict[str, Any]:
        with open('/inputs/dagger.json') as f:
            return json.loads(f.read())

    def _to_dict(self, obj):
        if dataclasses.is_dataclass(obj):
            return dataclasses.asdict(obj)
        if isinstance(obj, Sequence):
            return list(map(self._to_dict, obj))
        return obj

    def _serialize(self, obj) -> str:
        try:
            r = json.dumps(obj)
            return r
        except TypeError:
            # strawberry types are not serializable but can be converted to a dict
            return json.dumps(self._to_dict(obj))

    def _write_outputs(self, result: Any) -> None:
        with open('/outputs/dagger.json', 'w') as f:
            f.write(self._serialize(result))

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
        logging.debug('sdk inputs <- {}'.format(inputs))
        result = self._call_resolver(inputs)
        logging.debug('sdk outputs -> {}'.format(result))
        self._write_outputs(result)
