from dagger.client._session import Session, default_session


async def connect() -> Session:
    """Open the default session's connection."""
    return await default_session().connect()


async def close() -> None:
    """Close the default session's connection."""
    await default_session().close()
