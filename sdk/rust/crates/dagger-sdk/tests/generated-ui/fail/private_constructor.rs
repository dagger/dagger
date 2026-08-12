#[allow(dead_code, unused_imports)]
#[path = "../../fixtures/generated_client/mod.rs"]
mod dagger_client;

fn private_constructor(query: dagger_sdk::QueryBuilder) {
    let _ = dagger_client::minimal::Client::from_query(query);
}

fn main() {}
