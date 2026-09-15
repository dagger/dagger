Exceptions
==========

Custom Dagger exceptions.

.. currentmodule:: dagger

.. autoexception:: DaggerError


Client execution
----------------

.. autoexception:: QueryError
   :undoc-members:

.. autoexception:: ExecError
   :special-members:
   :exclude-members: __init__


Client connection
-----------------

.. autoexception:: ClientError
.. autoexception:: ClientConnectionError
.. autoexception:: TransportError
.. autoexception:: ExecuteTimeoutError
.. autoexception:: InvalidQueryError


Engine provisioning
-------------------

.. autoexception:: ProvisionError
.. autoexception:: DownloadError
.. autoexception:: SessionError


Warnings
--------

.. autoexception:: VersionMismatch
