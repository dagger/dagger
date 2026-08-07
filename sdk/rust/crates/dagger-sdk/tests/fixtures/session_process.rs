use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};

fn main() {
    let mode = std::env::var("DAGGER_TEST_SESSION_FIXTURE_MODE").unwrap_or_default();
    let token = std::env::var("DAGGER_TEST_SECRET").unwrap_or_default();
    match mode.as_str() {
        "valid" | "valid-fail" | "delayed-worker" => {
            eprintln!("sealed stderr contains {token}");
            println!(r#"{{"port":4321,"session_token":"{token}","future_field":true}}"#);
            print!("stdout suffix contains {token}\n");
            let _ = std::io::stdout().flush();
            let mut input = Vec::new();
            let _ = std::io::stdin().read_to_end(&mut input);
            if mode == "delayed-worker" {
                std::thread::sleep(std::time::Duration::from_millis(50));
            }
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
        "hung-stdin" | "ignore-termination" => {
            println!(r#"{{"port":4321,"session_token":"{token}"}}"#);
            let _ = std::io::stdout().flush();
            std::thread::sleep(std::time::Duration::from_secs(60));
        }
        "already-exited" => {
            println!(r#"{{"port":4321,"session_token":"{token}"}}"#);
            let _ = std::io::stdout().flush();
        }
        "pipe-failure" => {
            println!(r#"{{"port":4321,"session_token":"{token}"}}"#);
            let _ = std::io::stdout().flush();
            std::process::exit(9);
        }
        "engine" => run_engine_fixture(&token),
        _ => {}
    }
}

fn run_engine_fixture(token: &str) {
    let listener = match TcpListener::bind("127.0.0.1:0") {
        Ok(listener) => listener,
        Err(_) => std::process::exit(11),
    };
    let port = match listener.local_addr() {
        Ok(address) => address.port(),
        Err(_) => std::process::exit(12),
    };
    if listener.set_nonblocking(true).is_err() {
        std::process::exit(13);
    }
    println!(r#"{{"port":{port},"session_token":"{token}"}}"#);
    let _ = std::io::stdout().flush();

    let stopped = Arc::new(AtomicBool::new(false));
    let server_stopped = Arc::clone(&stopped);
    let server = std::thread::spawn(move || {
        while !server_stopped.load(Ordering::Acquire) {
            match listener.accept() {
                Ok((stream, _)) => serve_request(stream),
                Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => {
                    std::thread::sleep(std::time::Duration::from_millis(1));
                }
                Err(_) => break,
            }
        }
    });

    let mut input = Vec::new();
    let _ = std::io::stdin().read_to_end(&mut input);
    stopped.store(true, Ordering::Release);
    let _ = server.join();
}

fn serve_request(mut stream: TcpStream) {
    let _ = stream.set_read_timeout(Some(std::time::Duration::from_secs(2)));
    let mut request = Vec::new();
    let mut buffer = [0_u8; 4096];
    let mut expected_length = None;
    loop {
        let count = match stream.read(&mut buffer) {
            Ok(0) | Err(_) => break,
            Ok(count) => count,
        };
        request.extend_from_slice(&buffer[..count]);
        if expected_length.is_none()
            && let Some(header_end) = find_bytes(&request, b"\r\n\r\n")
        {
            let headers = String::from_utf8_lossy(&request[..header_end]);
            let content_length = headers.lines().find_map(|line| {
                let (name, value) = line.split_once(':')?;
                name.eq_ignore_ascii_case("content-length")
                    .then(|| value.trim().parse::<usize>().ok())
                    .flatten()
            });
            expected_length = Some(header_end + 4 + content_length.unwrap_or(0));
        }
        if expected_length.is_some_and(|length| request.len() >= length) {
            break;
        }
    }

    let body = br#"{"data":{"version":"v1.0.0-beta.10+25300124"}}"#;
    let response = format!(
        "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
        body.len()
    );
    let _ = stream.write_all(response.as_bytes());
    let _ = stream.write_all(body);
    let _ = stream.flush();
}

fn find_bytes(haystack: &[u8], needle: &[u8]) -> Option<usize> {
    haystack
        .windows(needle.len())
        .position(|window| window == needle)
}
