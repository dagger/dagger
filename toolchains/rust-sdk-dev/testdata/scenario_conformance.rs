//! Closed Rust runner for reviewed authority-scenario realizations.
//!
//! This source is injected into the exact installed SDK only for sign-off. The checked
//! realization registry binds its complete bytes, and the compile guard builds it during normal
//! Rust tests. Each reviewed realization adds one stable selector and one idiomatic Rust function;
//! unknown selectors fail before a Dagger session is opened.

use std::error::Error;

use dagger_sdk::Client;
use serde::Serialize;

const TARGET_REVISION: &str = "25300124ca110612edc09c43f89cb5fad6028170";
const TARGET_VERSION: &str = "v1.0.0-beta.10";

#[derive(Serialize)]
struct ScenarioObservation {
    realization_id: String,
    scenario_id: &'static str,
    realization_kind: &'static str,
    observation: String,
}

#[derive(Serialize)]
struct ScenarioObservationSet {
    format_version: u32,
    target_revision: &'static str,
    target_version: &'static str,
    observations: Vec<ScenarioObservation>,
}

pub(crate) const fn registered_realization_ids() -> &'static [&'static str] {
    &[]
}

async fn execute_realization(
    realization_id: &str,
    _client: &Client,
) -> Result<ScenarioObservation, Box<dyn Error>> {
    Err(std::io::Error::other(format!(
        "unknown Rust scenario realization {realization_id:?}"
    ))
    .into())
}

async fn run() -> Result<(), Box<dyn Error>> {
    let realization_id = std::env::var("DAGGER_RUST_SCENARIO_REALIZATION")
        .map_err(|_| "DAGGER_RUST_SCENARIO_REALIZATION is required")?;
    if !registered_realization_ids().contains(&realization_id.as_str()) {
        return Err(std::io::Error::other(format!(
            "unknown Rust scenario realization {realization_id:?}"
        ))
        .into());
    }

    let client = dagger_sdk::connect().await?;
    let observation = execute_realization(&realization_id, &client).await?;
    client.close().await?;
    println!(
        "{}",
        String::from_utf8(serde_json::to_vec(&ScenarioObservationSet {
            format_version: 1,
            target_revision: TARGET_REVISION,
            target_version: TARGET_VERSION,
            observations: vec![observation],
        })?)?
    );
    Ok(())
}

#[cfg(not(test))]
#[tokio::main]
async fn main() -> Result<(), Box<dyn Error>> {
    run().await
}
