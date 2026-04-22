use core::convert::TryInto;
use ml_dsa::signature::Verifier;
use ml_dsa::{EncodedSignature, EncodedVerifyingKey, MlDsa87, Signature, VerifyingKey};
use serde::Deserialize;
use serde_json::Value;

const MLDSA87_PK_LEN: usize = 2592;
const MLDSA87_SIG_LEN: usize = 4627;

#[derive(Debug, Deserialize)]
struct TxShape {
    id: String,
    sender: String,
    recipient: String,
    amount: serde_json::Value,
    #[serde(default)]
    fee: serde_json::Value,
    geotag: Option<String>,
    parent_cells: Vec<serde_json::Value>,
    signature: String,
}

fn basic_shape_ok(v: &TxShape) -> bool {
    if v.id.len() < 32 {
        return false;
    }
    if v.sender.is_empty() || v.recipient.is_empty() {
        return false;
    }
    if !matches!(v.amount, Value::Number(_)) {
        return false;
    }
    if v.fee != Value::Null && !matches!(v.fee, Value::Number(_)) {
        return false;
    }
    if let Some(ref g) = v.geotag {
        if g.len() > 256 {
            return false;
        }
    }
    let n = v.parent_cells.len();
    if n < 2 || n > 10 {
        return false;
    }
    let sig_len = v.signature.len();
    if sig_len < 100 || sig_len > 10_000 {
        return false;
    }
    if !v.signature.chars().all(|c| c.is_ascii_hexdigit()) {
        return false;
    }
    true
}

/// JSON body that was signed by the wallet / consensus path (Go `encoding/json` field order).
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

fn value_str<'a>(obj: &'a serde_json::Map<String, Value>, key: &str) -> Option<&'a str> {
    obj.get(key).and_then(Value::as_str)
}

fn value_field<'a>(obj: &'a serde_json::Map<String, Value>, key: &str) -> &'a Value {
    obj.get(key).unwrap_or(&Value::Null)
}

/// Reconstruct the exact UTF-8 bytes Go signs: `Transaction` / `TransactionData` with empty signature.
fn wallet_signing_bytes(obj: &serde_json::Map<String, Value>) -> Option<Vec<u8>> {
    let id = value_str(obj, "id")?;
    let sender = value_str(obj, "sender")?;
    let recipient = value_str(obj, "recipient")?;
    let amount = value_field(obj, "amount");
    let fee = obj
        .get("fee")
        .cloned()
        .unwrap_or_else(|| Value::Number(0u64.into()));
    let geotag = obj
        .get("geotag")
        .cloned()
        .unwrap_or_else(|| Value::String(String::new()));
    let parent_cells = value_field(obj, "parent_cells");
    let timestamp = obj
        .get("timestamp")
        .cloned()
        .unwrap_or_else(|| Value::String(String::new()));
    if !amount.is_number() {
        return None;
    }
    if !fee.is_null() && !fee.is_number() {
        return None;
    }
    if !parent_cells.is_array() {
        return None;
    }
    let body = WalletSigningBody {
        id,
        sender,
        recipient,
        amount,
        fee: &fee,
        geotag: &geotag,
        parent_cells,
        signature: "",
        timestamp: &timestamp,
    };
    serde_json::to_vec(&body).ok()
}

fn verify_mldsa87_with_public_key(tx: &[u8]) -> bool {
    let root: Value = match serde_json::from_slice(tx) {
        Ok(v) => v,
        Err(_) => return false,
    };
    let obj = match root.as_object() {
        Some(m) => m,
        None => return false,
    };
    let pk_hex = match obj.get("public_key").and_then(Value::as_str) {
        Some(s) if !s.is_empty() => s,
        _ => return false,
    };
    if !pk_hex.chars().all(|c| c.is_ascii_hexdigit()) || pk_hex.len() % 2 != 0 {
        return false;
    }
    let pk_bytes = match hex::decode(pk_hex.as_bytes()) {
        Ok(b) => b,
        Err(_) => return false,
    };
    if pk_bytes.len() != MLDSA87_PK_LEN {
        return false;
    }
    let sig_hex = match obj.get("signature").and_then(Value::as_str) {
        Some(s) => s,
        None => return false,
    };
    if !sig_hex.chars().all(|c| c.is_ascii_hexdigit()) || sig_hex.len() % 2 != 0 {
        return false;
    }
    let sig_bytes = match hex::decode(sig_hex.as_bytes()) {
        Ok(b) => b,
        Err(_) => return false,
    };
    if sig_bytes.len() != MLDSA87_SIG_LEN {
        return false;
    }
    let msg = match wallet_signing_bytes(obj) {
        Some(m) => m,
        None => return false,
    };
    let pk_arr: EncodedVerifyingKey<MlDsa87> = match pk_bytes.as_slice().try_into() {
        Ok(a) => a,
        Err(_) => return false,
    };
    let vk = VerifyingKey::<MlDsa87>::decode(&pk_arr);
    let sig_arr: EncodedSignature<MlDsa87> = match sig_bytes.as_slice().try_into() {
        Ok(a) => a,
        Err(_) => return false,
    };
    let sig = match Signature::<MlDsa87>::decode(&sig_arr) {
        Some(s) => s,
        None => return false,
    };
    vk.verify(&msg, &sig).is_ok()
}

fn validate_tx_bytes(tx: &[u8]) -> bool {
    if tx.is_empty() || tx.len() > 1_000_000 {
        return false;
    }
    let root: Value = match serde_json::from_slice(tx) {
        Ok(v) => v,
        Err(_) => return false,
    };
    let obj = match root.as_object() {
        Some(o) => o,
        None => return false,
    };
    if obj.contains_key("public_key") && obj.get("public_key").and_then(Value::as_str).is_some() {
        let v: TxShape = match serde_json::from_value(root.clone()) {
            Ok(v) => v,
            Err(_) => return false,
        };
        if !basic_shape_ok(&v) {
            return false;
        }
        return verify_mldsa87_with_public_key(tx);
    }
    let v: TxShape = match serde_json::from_slice(tx) {
        Ok(v) => v,
        Err(_) => return false,
    };
    basic_shape_ok(&v)
}

/// C ABI for hosts (e.g. Go wazero) — no wasm-bindgen imports required.
#[no_mangle]
pub extern "C" fn validate_raw(tx_ptr: *const u8, tx_len: usize) -> bool {
    if tx_ptr.is_null() || tx_len == 0 {
        return false;
    }
    let tx = unsafe { std::slice::from_raw_parts(tx_ptr, tx_len) };
    validate_tx_bytes(tx)
}

#[cfg(test)]
mod tests {
    use super::*;
    use ml_dsa::signature::{Keypair, Signer};
    use ml_dsa::{KeyGen, MlDsa87, Seed};
    use serde_json::{json, Map, Value};

    #[test]
    fn mldsa87_preflight_accepts_signed_wallet_json() {
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

        let signing = wallet_signing_bytes(&m).expect("signing json");
        let sig = kp
            .signing_key()
            .try_sign(&signing)
            .expect("ml-dsa sign");
        let sig_hex = hex::encode(sig.encode().as_slice());

        m.insert("signature".into(), Value::String(sig_hex));
        m.insert("public_key".into(), Value::String(pk_hex));

        let full = serde_json::to_vec(&Value::Object(m.clone())).expect("full json");
        assert!(
            validate_tx_bytes(&full),
            "validate_tx_bytes should accept ML-DSA-87 signed tx with public_key"
        );

        m.insert("amount".into(), json!(999.0));
        let tampered = serde_json::to_vec(&Value::Object(m)).expect("tampered json");
        assert!(
            !validate_tx_bytes(&tampered),
            "tampered amount must fail ML-DSA verification"
        );
    }

    #[test]
    fn legacy_shape_without_public_key_still_ok() {
        let j = json!({
            "id": "cccccccccccccccccccccccccccccccc",
            "sender": "sender_addr_here________________",
            "recipient": "recipient_addr_here_______________",
            "amount": 1.0,
            "fee": 0.01,
            "geotag": "US",
            "parent_cells": [
                "aaabbbcccdddeeefffggghhhhiiiijjj",
                "bbbaaacccdddfffeeeggghhhhjjjjiii"
            ],
            "signature": "ab".repeat(50),
        });
        let raw = serde_json::to_vec(&j).unwrap();
        assert!(validate_tx_bytes(&raw));
    }
}
