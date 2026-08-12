#[allow(dead_code, unused_imports)]
#[path = "../../fixtures/generated_client/mod.rs"]
mod dagger_client;

fn missing_import(client: &dagger_sdk::Client) {
    let _ = client.minimal();
}

fn main() {}
