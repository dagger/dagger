use std::io::{Read, Write};

fn main() {
    let mode = std::env::var("DAGGER_TEST_SESSION_FIXTURE_MODE").unwrap_or_default();
    let token = std::env::var("DAGGER_TEST_SECRET").unwrap_or_default();
    match mode.as_str() {
        "valid" | "valid-fail" => {
            eprintln!("sealed stderr contains {token}");
            println!(r#"{{"port":4321,"session_token":"{token}","future_field":true}}"#);
            print!("stdout suffix contains {token}\n");
            let _ = std::io::stdout().flush();
            let mut input = Vec::new();
            let _ = std::io::stdin().read_to_end(&mut input);
            eprintln!("stderr tail contains {token}");
            if mode == "valid-fail" {
                std::process::exit(7);
            }
        }
        "malformed-wait" => {
            println!(r#"{{"port":0,"session_token":"{token}"}}"#);
            let _ = std::io::stdout().flush();
            let mut input = Vec::new();
            let _ = std::io::stdin().read_to_end(&mut input);
        }
        "oversize" => {
            let bytes = vec![b'a'; 64 * 1024];
            let mut stdout = std::io::stdout().lock();
            let _ = stdout.write_all(&bytes);
            let _ = stdout.write_all(b"\n");
        }
        _ => {}
    }
}
