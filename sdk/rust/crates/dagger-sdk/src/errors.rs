use thiserror::Error;

#[derive(Error, Debug)]
pub enum ConnectError {
    #[error("failed to connect to dagger engine")]
    FailedToConnect(#[source] eyre::Error),
}

#[derive(Error, Debug)]
pub enum DaggerError {
    #[error("failed to build dagger internal graph")]
    Build(#[source] eyre::Error),
    #[error("failed to parse input type")]
    Serialize(#[source] eyre::Error),
    #[error("failed to query dagger engine: {0}")]
    Query(#[source] crate::core::graphql_client::GraphQLError),
    #[error("failed to unpack response")]
    Unpack(#[source] DaggerUnpackError),
    #[error("failed to download client")]
    DownloadClient(#[source] eyre::Error),
}

#[derive(Error, Debug)]
pub enum DaggerUnpackError {
    #[error("Too many nested objects inside graphql response")]
    TooManyNestedObjects,
    #[error("failed to deserialize response")]
    Deserialize(#[source] serde_json::Error),
}
