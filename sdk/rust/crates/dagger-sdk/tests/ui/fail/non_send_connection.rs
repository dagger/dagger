use std::rc::Rc;

use async_trait::async_trait;
use dagger_sdk::{EngineConnection, EngineConnectionError, RawRequest, RawResponse};

struct LocalConnection(Rc<()>);

#[async_trait]
impl EngineConnection for LocalConnection {
    async fn execute(&self, _request: RawRequest) -> Result<RawResponse, EngineConnectionError> {
        unimplemented!()
    }

    async fn close(&self) -> Result<(), EngineConnectionError> {
        Ok(())
    }

    fn abort(&self) {}
}

fn main() {}
