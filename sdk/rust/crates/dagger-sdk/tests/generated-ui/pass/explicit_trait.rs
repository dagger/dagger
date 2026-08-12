#[allow(dead_code, unused_imports)]
#[path = "../../fixtures/generated_client/mod.rs"]
mod dagger_client;

use dagger_client::MinimalExt as _;

async fn explicit_surface(
    query: &dagger_sdk::QueryBuilder,
    id: dagger_sdk::Id,
) -> Result<(), dagger_sdk::QueryError> {
    let root: dagger_client::minimal::Client = query.minimal();
    let _: String = root
        .search_opts(
            dagger_client::minimal::SearchOpts::default()
                .with_enabled(false)
                .with_count(0)
                .with_label(String::new())
                .with_item_null(),
        )
        .await?;
    let _: String = root
        .use_items(vec![Some(dagger_sdk::IdInput::new(id)), None])
        .await?;
    Ok(())
}

fn main() {}
