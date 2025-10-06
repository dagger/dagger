use crate::core::introspection::IntrospectionResponse;
use crate::core::{config::Config, engine::Engine, session::Session};

pub async fn get_schema() -> eyre::Result<IntrospectionResponse> {
    let cfg = Config::default();

    //TODO: Implement context for proc
    let (conn, _proc) = Engine::new().start(&cfg).await?;
    let session = Session::new();
    let req_builder = session.start(&cfg, &conn)?;
    let schema = session.schema(req_builder).await?;

    Ok(schema)
}
