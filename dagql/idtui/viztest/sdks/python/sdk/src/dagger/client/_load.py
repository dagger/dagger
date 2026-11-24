"""How the module a target names gets served into a session."""

from dagger.client._core import Arg, Context
from dagger.client._descriptor import Target
from dagger.client._session import Session

# A descriptor has one shape for a git ref and for a workspace path: `ref` is
# the address, `pin` the refPin. There is no name to pin: the engine derives
# it from the module's own config, as it did for the schema this client was
# generated from, so the two agree unless the module renamed itself since,
# and then the client is stale and its first selection says so (see
# stale_client_error).
#
# A local path needs nothing more under a module entrypoint: the engine
# resolves it in the tree of the module whose code runs in this process.


async def load_target(session: Session, target: Target) -> None:
    """Serve the module a target names."""
    ctx = Context(session)
    # An engine without the field, below this SDK's floor, fails the load
    # here: a ClientLoadError, not a stale client, since regenerating the
    # client cannot give the engine a field.
    args = [Arg("address", target.ref), Arg("refPin", target.pin, None)]
    await ctx.root_select("serveModule", args).execute()
