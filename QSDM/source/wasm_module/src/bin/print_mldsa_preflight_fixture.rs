//! One-shot: `cargo run --bin print_mldsa_preflight_fixture` (host target) writes golden JSON for Go tests.
use ml_dsa::signature::{Keypair, Signer};
use ml_dsa::{KeyGen, MlDsa87, Seed};
use serde_json::{json, Map, Value};

fn main() {
    let seed = Seed::default();
    let kp = MlDsa87::from_seed(&seed);
    let vk = kp.verifying_key();
    let pk_hex = hex::encode(vk.encode().as_slice());

    let mut m = Map::new();
    m.insert(
        "id".into(),
        Value::String("cccccccccccccccccccccccccccccccc".into()),
    );
    m.insert(
        "sender".into(),
        Value::String("sender_addr_here________________".into()),
    );
    m.insert(
        "recipient".into(),
        Value::String("recipient_addr_here_______________".into()),
    );
    m.insert("amount".into(), json!(1.0));
    m.insert("fee".into(), json!(0.01));
    m.insert("geotag".into(), Value::String("US".into()));
    m.insert(
        "parent_cells".into(),
        json!([
            "aaabbbcccdddeeefffggghhhhiiiijjj",
            "bbbaaacccdddfffeeeggghhhhjjjjiii"
        ]),
    );
    m.insert(
        "timestamp".into(),
        Value::String("2020-01-02T15:04:05Z".into()),
    );

    #[derive(serde::Serialize)]
    struct WalletSigningBody<'a> {
        id: &'a str,
        sender: &'a str,
        recipient: &'a str,
        amount: &'a Value,
        fee: &'a Value,
        geotag: &'a Value,
        parent_cells: &'a Value,
        signature: &'static str,
        timestamp: &'a Value,
    }
    let id = m.get("id").and_then(Value::as_str).unwrap();
    let sender = m.get("sender").and_then(Value::as_str).unwrap();
    let recipient = m.get("recipient").and_then(Value::as_str).unwrap();
    let amount = m.get("amount").unwrap();
    let fee = m.get("fee").unwrap();
    let geotag = m.get("geotag").unwrap();
    let parent_cells = m.get("parent_cells").unwrap();
    let timestamp = m.get("timestamp").unwrap();
    let signing = serde_json::to_vec(&WalletSigningBody {
        id,
        sender,
        recipient,
        amount,
        fee,
        geotag,
        parent_cells,
        signature: "",
        timestamp,
    })
    .unwrap();

    let sig = kp.signing_key().try_sign(&signing).unwrap();
    let sig_hex = hex::encode(sig.encode().as_slice());
    m.insert("signature".into(), Value::String(sig_hex));
    m.insert("public_key".into(), Value::String(pk_hex));

    let full = serde_json::to_vec(&Value::Object(m)).unwrap();
    println!("{}", String::from_utf8_lossy(&full));
}
