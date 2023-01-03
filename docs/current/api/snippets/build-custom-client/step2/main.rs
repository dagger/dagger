use base64::encode;
use gql_client::Client;
use serde_json::Value;
use std::collections::HashMap;
use std::env;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let port = env::var("DAGGER_SESSION_PORT").expect("$DAGGER_SESSION_PORT doesn't exist");
    let token = env::var("DAGGER_SESSION_TOKEN").expect("$DAGGER_SESSION_TOKEN doesn't exist");
    let query = r#"
    query {
      container {
        from (address: "alpine:latest") {
          withExec(args:["uname", "-nrio"]) {
            stdout
          }
        }
      }
    }
 "#;

    let mut headers = HashMap::new();
    headers.insert(
        "authorization",
        format!("Basic {}", encode(format!("{}:", token))),
    );
    let client = Client::new_with_headers(format!("http://127.0.0.1:{}/query", port), headers);
    let data = client.query_unwrap::<Value>(query).await.unwrap();

    println!(
        "{}",
        data["container"]["from"]["withExec"]["stdout"]
            .as_str()
            .unwrap()
    );

    Ok(())
}
