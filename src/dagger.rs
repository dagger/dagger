use std::sync::Arc;

pub fn connect() -> eyre::Result<Client> {
    Client::new()
}

struct InnerClient {}

#[allow(dead_code)]
pub struct Client {
    inner: Arc<InnerClient>,
}

impl Client {
    pub fn new() -> eyre::Result<Self> {
        Ok(Self {
            inner: Arc::new(InnerClient {}),
        })
    }

    //    pub fn container(&self) -> Container {}
}
