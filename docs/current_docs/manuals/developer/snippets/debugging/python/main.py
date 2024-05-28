import logging

from dagger import function, object_type
from dagger.log import configure_logging

configure_logging(logging.DEBUG)


@object_type
class MyModule:
    @function
    def echo(self, msg: str) -> str:
        return msg
