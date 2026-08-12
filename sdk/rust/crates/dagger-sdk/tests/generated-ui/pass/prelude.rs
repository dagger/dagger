#[allow(dead_code, unused_imports)]
#[path = "../../fixtures/generated_client/mod.rs"]
mod dagger_client;

use dagger_client::prelude::*;

async fn generated_surface(client: &dagger_client::Client) -> Result<(), dagger_sdk::QueryError> {
    let root = client.minimal();
    let _: &dagger_sdk::QueryBuilder = root.selection();
    let node = root.node();
    let _: &dagger_sdk::QueryBuilder = dagger_client::minimal::Node::selection(&node);
    let _: dagger_client::minimal::Token = "token".into();
    let _: dagger_client::minimal::State = dagger_client::minimal::State::Ready;
    let _: dagger_client::minimal::Config =
        dagger_client::minimal::Config::new().with_enabled(false);
    let local = root.helper();
    let _: dagger_sdk::Id = local.id().await?;
    let core: dagger_sdk::Container = root.container();
    let _: dagger_sdk::Id = core.id().await?;
    let item = root.item().await?;
    if let Some(item) = item {
        let _: String = root.use_item(item).await?;
    }
    Ok(())
}

fn main() {}
