use crate::introspection::IntrospectionResponse;
use crate::{config::Config, engine::Engine, session::Session};

pub async fn get_schema() -> eyre::Result<IntrospectionResponse> {
    let cfg = Config::new(None, None, None, None);

    //TODO: Implement context for proc
    let (conn, _proc) = Engine::new().start(&cfg).await?;
    let session = Session::new();
    let req_builder = session.start(&cfg, &conn)?;
    let schema = session.schema(req_builder).await?;

    Ok(schema)
}

#[cfg(test)]
mod tests {
    use super::get_schema;

    #[tokio::test]
    async fn can_get_schema() {
        let _ = get_schema().await.unwrap();
    }
}
