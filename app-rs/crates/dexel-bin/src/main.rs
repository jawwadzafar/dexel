// dexel-bin — P0a cross-compile probe.
//
// Genuinely exercises the dependency surface app-rs/ would plausibly use
// (dev_docs/rust-port-evaluation.md §2.1 / §5.0), not a hello-world stub:
// a real SQLite table + PRAGMA user_version (rusqlite, bundled), a real
// HMAC (hmac+sha2), a real embedded asset (rust-embed) served over a real
// HTTP request (tiny_http), and a real OS path lookup (dirs).
//
// One route: GET /api/health -> JSON. GET / -> the embedded test file.

use hmac::{Hmac, Mac};
use rust_embed::RustEmbed;
use serde_json::json;
use sha2::Sha256;
use tiny_http::{Header, Method, Response, Server};

#[derive(RustEmbed)]
#[folder = "embed/"]
struct Embed;

fn open_db() -> rusqlite::Result<rusqlite::Connection> {
    let conn = rusqlite::Connection::open_in_memory()?;
    conn.execute_batch(
        "PRAGMA user_version = 1;
         CREATE TABLE probe (id INTEGER PRIMARY KEY, note TEXT NOT NULL);
         INSERT INTO probe (note) VALUES ('p0a cross-compile probe');",
    )?;
    let user_version: i64 = conn.query_row("PRAGMA user_version", [], |r| r.get(0))?;
    println!("sqlite PRAGMA user_version = {user_version}");
    Ok(conn)
}

fn hmac_hex(key: &[u8], msg: &[u8]) -> String {
    let mut mac = Hmac::<Sha256>::new_from_slice(key).expect("hmac key of any length");
    mac.update(msg);
    let bytes = mac.finalize().into_bytes();
    bytes.iter().map(|b| format!("{b:02x}")).collect()
}

fn main() {
    // Real SQLite exercise.
    let _conn = open_db().expect("sqlite probe failed");

    // Real HMAC exercise.
    let mac_hex = hmac_hex(b"probe-key", b"probe-message");
    println!("hmac-sha256 = {mac_hex}");

    // Real OS path lookup exercise (dirs).
    let home = dirs::home_dir();
    println!("home_dir = {home:?}");

    // Real embedded asset.
    let asset = Embed::get("hello.txt").expect("embedded test file missing");
    let body = String::from_utf8_lossy(&asset.data).into_owned();

    let addr = std::env::var("DEXEL_BIN_ADDR").unwrap_or_else(|_| "127.0.0.1:0".to_string());
    let server = Server::http(&addr).expect("failed to bind");
    println!("DEXEL_LISTENING http://{}", server.server_addr());

    // Serve exactly one real request then exit, so the probe is a real
    // TCP round-trip and not just a bound-and-quit smoke test.
    if let Ok(Some(request)) = server.recv_timeout(std::time::Duration::from_secs(5)) {
        let response = match (request.method(), request.url()) {
            (Method::Get, "/api/health") => {
                let payload = json!({
                    "ok": true,
                    "source": "dexel-bin-probe",
                    "hmac": mac_hex,
                });
                let header =
                    Header::from_bytes(&b"Content-Type"[..], &b"application/json"[..]).unwrap();
                Response::from_string(payload.to_string()).with_header(header)
            }
            _ => {
                let header = Header::from_bytes(&b"Content-Type"[..], &b"text/plain"[..]).unwrap();
                Response::from_string(body).with_header(header)
            }
        };
        let _ = request.respond(response);
    } else {
        println!("no request received within timeout (expected when run non-interactively)");
    }
}
