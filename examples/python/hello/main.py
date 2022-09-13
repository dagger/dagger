import sys
import json
import types
import inspect
from typing import NewType, Any

import graphql
import strawberry
from strawberry import Schema

# FSID = strawberry.scalar(
#     NewType('FSID', str),
#     serialize=lambda v: v,
#     parse_value=lambda v: v,
# )

# SecretID = strawberry.scalar(
#     NewType('SecretID', str),
#     serialize=lambda v: v,
#     parse_value=lambda v: v,
# )

# Filesystem = NewType('Filesystem', object)
# Exec = NewType('Exec', object)


# @strawberry.type
# class Filesystem:
#     id: FSID
#     exec: Exec
#     dockerbuild: Filesystem
#     file: str


# @strawberry.type
# class Exec:
#     fs: Filesystem
#     stdout: str
#     stderr: str
#     exitcode: int
#     mount: Filesystem


@strawberry.type
class Hello:

    lang: str = "test"

    @strawberry.field
    def say(self, msg: str) -> str:
        return 'Hello {}!'.format(msg)


@strawberry.type
class Query:
    hello: Hello


def read_inputs() -> dict[str, Any]:
    with open('/inputs/dagger.json') as f:
        return json.loads(f.read())


def call_resolver(inputs: dict) -> Any:
    def check(x):
        if x not in inputs:
            raise RuntimeError('missing {}'.format(x))

    for i in ['args', 'parent', 'resolver']:
        check(i)

    split = inputs['resolver'].split('.', 2)
    typ, field = split[0], split[1]
    obj = getattr(sys.modules[__name__], typ)

    if obj is Query:
        # FIXME: find a generic way to handle this, this won't work for a top-level resolver
        return inputs['args']

    # if we have parent args, pass them to init the object
    inst = obj(**inputs['parent']) if inputs['parent'] else obj()
    resolver = getattr(inst, field)

    if callable(resolver):
        return resolver(**inputs['args'])

    return resolver


if __name__ == '__main__':
    inputs = read_inputs()
    print('### inputs <- {}'.format(inputs))
    result = call_resolver(inputs)
    print('### outputs -> {}'.format(result))
    with open('/outputs/dagger.json', 'w') as f:
        f.write(json.dumps(result))

    # # i = {'args': {}, 'parent': None, 'resolver': 'Query.hello'}
    # inputs = {'args': {'msg': 'test1'}, 'parent': {}, 'resolver': 'Hello.say'}

    # result = call_resolver(inputs)
    # print(result)

    # result = call_resolver(i1)
    # print(result)
