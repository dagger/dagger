Connection
==========

.. currentmodule:: dagger

.. autoclass:: Config
   :no-show-inheritance:

.. autoclass:: Connection
   :no-show-inheritance:


Experimental
------------

.. warning::
   These functions are part of an experimental feature to use a globally available client instead of getting an instance from a connection. Their interfaces and availability may change in the future.

.. autofunction:: connection

.. autofunction:: closing

.. autofunction:: connect

    Connect to a Dagger Engine using the global client.

    Similar to :py:func:`dagger.closing` but establishes the connection
    explicitly rather than on first use.

    Example::

        import anyio
        import dagger

        async def main():
            async with await dagger.connect():
                ctr = dagger.container().from_("python:3.11.1-alpine")
                # Connection is only established when needed.
                version = await ctr.with_exec(["python", "-V"]).stdout()

            # Connection is closed when leaving the context manager's scope.

            print(version)
            # Output: Python 3.11.1

        anyio.run(main)
