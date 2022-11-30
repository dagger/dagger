use gql_client::Client;
use serde_json::Value;
use std::env;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
  let endpoint = env::var("DAGGER_SESSION_URL").expect("$DAGGER_SESSION_URL doesn't exist");
  let query = r#"
    query {
      container {
        from (address:"alpine:latest") {
          withExec(args:["uname", "-nrio"]) {
            stdout
          }
        }
      }
    }
  "#;

  let client = Client::new(endpoint);
  let data = client.query_unwrap::<Value>(query).await.unwrap();

  println!(
    "{}",
    data["container"]["from"]["withExec"]["stdout"]
        .as_str()
        .unwrap()
  );
  Ok(())
}
