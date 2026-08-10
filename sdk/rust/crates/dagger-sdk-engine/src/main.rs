#![deny(warnings)]
#![deny(unsafe_code)]
//! Command-line boundary for the private Rust engine operation tool.

#[tokio::main]
async fn main() {
    if let Err(diagnostic) = dagger_sdk_engine::cli::run_from(std::env::args_os()).await {
        eprintln!("{}", dagger_sdk_engine::cli::render_diagnostic(&diagnostic));
        std::process::exit(1);
    }
}
