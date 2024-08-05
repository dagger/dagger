# Make sure to place exceptions first as they're dependencies of other imports.
import contextlib
from dagger._exceptions import VersionMismatch as VersionMismatch
from dagger._exceptions import DaggerError as DaggerError
from dagger._exceptions import ProvisionError as ProvisionError
from dagger._exceptions import DownloadError as DownloadError
from dagger._exceptions import SessionError as SessionError
from dagger._exceptions import ClientError as ClientError
from dagger._exceptions import ClientConnectionError as ClientConnectionError
from dagger._exceptions import TransportError as TransportError
from dagger._exceptions import ExecuteTimeoutError as ExecuteTimeoutError
from dagger._exceptions import InvalidQueryError as InvalidQueryError
from dagger._exceptions import QueryError as QueryError
from dagger._exceptions import ExecError as ExecError

# Make sure Config is first as it's a dependency in Connection.
from dagger._config import Config as Config
from dagger._config import Retry as Retry
from dagger._config import Timeout as Timeout

# We need the star import since this is a generated module.
from dagger.client.gen import *
from dagger._connection import Connection as Connection
from dagger._connection import connection as connection
from dagger._connection import connect as connect
from dagger._connection import close as close

# Modules.
from dagger.mod import Arg as Arg
from dagger.mod import DefaultPath as DefaultPath
from dagger.mod import Doc as Doc
from dagger.mod import Ignore as Ignore
from dagger.mod import Enum as Enum
from dagger.mod import Name as Name
from dagger.mod import enum_type as enum_type
from dagger.mod import field as field
from dagger.mod import function as function
from dagger.mod import object_type as object_type

# Re-export imports so they look like they live directly in this package.
for _value in list(locals().values()):
    if getattr(_value, "__module__", "").startswith("dagger."):
        with contextlib.suppress(AttributeError):
            _value.__module__ = __name__
