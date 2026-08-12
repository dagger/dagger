#[allow(dead_code, unused_imports)]
#[path = "../../fixtures/generated_client/mod.rs"]
mod dagger_client;

async fn wrong_identifier(root: &dagger_client::minimal::Client) {
    let _ = root.use_item(root.helper()).await;
}

fn main() {}
