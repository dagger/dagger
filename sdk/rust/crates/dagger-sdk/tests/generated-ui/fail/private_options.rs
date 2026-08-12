#[allow(dead_code, unused_imports)]
#[path = "../../fixtures/generated_client/mod.rs"]
mod dagger_client;

fn private_option_carriers() {
    let _ = dagger_client::minimal::SearchOpts {
        enabled: Some(Some(false)),
        count: Some(Some(0)),
        label: Some(Some(String::new())),
        item: None,
    };
}

fn main() {}
