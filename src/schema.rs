use graphql_introspection_query::introspection_response::IntrospectionResponse;

use crate::{config::Config, engine::Engine, session::Session};

pub fn get_schema() -> eyre::Result<IntrospectionResponse> {
    let cfg = Config::new(None, None, None, None);

    //TODO: Implement context for proc
    let (conn, proc) = Engine::new().start(&cfg)?;
    let session = Session::new();
    let req_builder = session.start(cfg, &conn)?;
    let schema = session.schema(req_builder)?;

    Ok(schema)
}

#[cfg(test)]
mod tests {
    use super::get_schema;

    #[test]
    fn can_get_schema() {
        let _ = get_schema().unwrap();
    }
}
