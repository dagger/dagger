use core::time;
use std::thread::sleep;

use graphql_introspection_query::introspection_response::IntrospectionResponse;

use crate::{config::Config, engine::Engine, session::Session};

pub fn get_schema() -> eyre::Result<IntrospectionResponse> {
    //TODO: Implement cotext for proc
    let cfg = Config::new(None, None, None, None);
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
