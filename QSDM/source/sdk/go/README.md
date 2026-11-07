# QSDM Go SDK

Go client for the public HTTP API of a QSDM node (for example
`https://api.qsdm.tech`). It depends only on the Go standard library. The
JavaScript SDK (`QSDM/source/sdk/javascript`, npm `qsdm-sdk`) offers the same
transfer surface.

```bash
go get github.com/blackbeardONE/QSDM/QSDM/source/sdk/go
```

The doubled path is correct: the repository is `github.com/blackbeardONE/QSDM`
and this package is its own module at `QSDM/source/sdk/go`.

## Quick start

```go
package main

import (
	"context"
	"fmt"

	qsdm "github.com/blackbeardONE/QSDM/QSDM/source/sdk/go"
)

func main() {
	client := qsdm.NewClient("https://api.qsdm.tech")
	st, err := client.GetNodeStatus(context.Background())
	if err != nil {
		panic(err)
	}
	fmt.Println(st.Version, st.ChainTip)
}
```

Public routes need no credentials.

## Sending CELL

A node accepts a CELL transfer from a client only as an envelope signed with
**ML-DSA-87**, posted to `POST /api/v1/wallet/submit-signed` (Ed25519
transactions were retired on 2026-10-16). Since block 816,600 every
transfer must also name **Proof-of-Entanglement (PoE) parents**: two to ten
transactions committed in the last 8,640 blocks (about 24 hours). A transfer
without valid parents is refused with HTTP 422
(`proof-of-entanglement: ...`). See `QSDM/docs/docs/PROOF_OF_ENTANGLEMENT.md`.

The SDK never holds a private key. It builds the envelope, asks a signer you
supply to sign it, and submits it:

| Step | Method | Route |
|---|---|---|
| Next nonce | `GetWalletNonce` (`Next`) | `GET /api/v1/wallet/nonce?sender=` |
| PoE parents | `GetPoEParents` | `GET /api/v1/chain/parents` (falls back to `GET /api/v1/receipts` only when that route is missing: 404, 405 or 501) |
| Unsigned envelope | `PrepareTransfer` | the two reads above |
| Bytes to sign | `TransferEnvelope.SigningBytes` | none (identical to the node's `pkg/wallet` encoding) |
| Signature | `SignTransfer` with your `TransferSigner` | none |
| Submit | `SubmitSignedTransfer` | `POST /api/v1/wallet/submit-signed` |
| All of the above | `SendTransfer` | |
| Follow it | `GetReceipt` | `GET /api/v1/receipts/{tx_id}` |

### With your own key: `TransferSigner`

`TransferSigner` has two methods: `PublicKey()` returns the raw ML-DSA-87
public key (2,592 bytes) and `Sign(ctx, message)` returns a raw ML-DSA-87
(FIPS 204) signature (4,627 bytes) over `message` with an empty context
string. Every node verifies it with `mldsa87.Verify(publicKey, message, nil,
signature)` from `github.com/cloudflare/circl`, the library `qsdmcli` signs
with. A signer built on that package (add it to your own module; this SDK
does not depend on it):

```go
import (
	"context"

	qsdm "github.com/blackbeardONE/QSDM/QSDM/source/sdk/go"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

type circlSigner struct {
	sk  *mldsa87.PrivateKey
	pub []byte
}

func (s circlSigner) PublicKey() []byte { return s.pub }

func (s circlSigner) Sign(_ context.Context, message []byte) ([]byte, error) {
	sig := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(s.sk, message, nil, true, sig); err != nil {
		return nil, err
	}
	return sig, nil
}

func send(ctx context.Context, rawPrivateKey []byte) error {
	var sk mldsa87.PrivateKey
	if err := sk.UnmarshalBinary(rawPrivateKey); err != nil { // 4,896 bytes
		return err
	}
	signer := circlSigner{sk: &sk, pub: sk.Public().(*mldsa87.PublicKey).Bytes()}

	client := qsdm.NewClient("https://api.qsdm.tech")
	out, err := client.SendTransfer(ctx, qsdm.TransferRequest{
		Recipient: "1111111111111111111111111111111111111111111111111111111111111111",
		Amount:    1.25, // CELL, at most 8 decimals
	}, signer)
	if err != nil {
		return err
	}
	// out.Status is "pending" (HTTP 202): queued for the next block.
	receipt, err := client.GetReceipt(ctx, out.TransactionID) // 404 until it is in a block
	_ = receipt
	return err
}
```

The sender address is `WalletAddress(publicKey)`, the lowercase hex SHA-256
of the public key. `SendTransfer` derives it from the signer when
`TransferRequest.Sender` is empty. A hardware module or a remote signing
service can implement the same interface. How you store the key is up to you;
the SDK never asks for it.

### With `qsdmcli`, Hive or the web wallet

Keys created by QSDM Hive, the web wallet and `qsdmcli` stay in those tools.
Use them to sign, and the SDK to submit:

```bash
# unsigned.json: {"id":"...","sender":"<your address>","recipient":"...",
#                 "amount":1.25,"fee":0,"geotag":"","parent_cells":[],"timestamp":"..."}
qsdmcli wallet sign-tx --auto-nonce --auto-parents \
    --api-url https://api.qsdm.tech --envelope-file unsigned.json > signed.json
```

```go
var env qsdm.TransferEnvelope
if err := json.Unmarshal(signedJSON, &env); err != nil {
	return err
}
out, err := client.SubmitSignedTransfer(ctx, env)
```

`PrepareTransfer` can produce `unsigned.json` for you (it fills the nonce and
the parents; `--auto-nonce --auto-parents` then refresh them). Parents expire,
so sign shortly before you submit.

### Errors

`SubmitSignedTransfer` and `SendTransfer` return a `*TransferError` when the
node refuses the transfer. It wraps `*ErrAPI`, so `IsNotFound` and
`IsUnauthorized` still work, and its `Error()` text says what to do next.

| `Reason` | HTTP | Meaning |
|---|---|---|
| `RejectPoEParents` (`IsPoERejection`) | 422 | Parents missing, malformed or outside the 8,640-block window. Call `SendTransfer` again: it fetches fresh parents. |
| `RejectSignature` | 422 | The signature does not verify over `SigningBytes()`. |
| `RejectNonce` | 409 | Nonce replay or gap. Prepare again with a fresh nonce. |
| `RejectDuplicate` | 409 | The node already has this transfer ID, or another transfer with this nonce is waiting for the next block. Follow it with `GetReceipt`; one transfer per sender per block. |
| `RejectInsufficientBalance` | 402 | Balance below amount + fee. |
| `RejectInvalid` | 400 | Validation failed (address, nonce 0, sender not `hex(sha256(public_key))`, ...). |
| `RejectRateLimited` | 429 | Too many submissions from your IP address (limits: `QSDM/docs/docs/API_REFERENCE.md`). |
| `RejectUnavailable` | 501, 503 | Queue full, read-only follower or writes paused. |
| `RejectUnauthorized`, `RejectPolicy`, `RejectOther` | other | See `Message`. |

`SendTransfer` makes one attempt; retry it yourself after a PoE or nonce
refusal (every call reads a fresh nonce and fresh parents).

## QSDM-20 tokens

QSDM-20 tokens (specification: `QSDM/docs/docs/QSDM_20.md`) are created,
sent, minted, burned and renounced with transactions signed by the same
ML-DSA-87 key that holds your CELL, so the same `TransferSigner` signs them.
The signature covers a 32-byte digest of the sender, the nonce, the fee and
the action's fields. Nodes refuse token transactions until the network sets
an activation height; `consensus_auth.qsdm20_active` in `GET /api/v1/status`
says when they are on.

```go
create := qsdm.QSDM20Action{
	Action:        qsdm.QSDM20ActionCreate,
	Symbol:        "GROOT",
	Name:          "Groot Coin",
	Decimals:      qsdm.QSDM20Decimals(8),
	MaxSupply:     "2100000000000000", // base units: 21 million tokens of 8 decimals
	InitialSupply: "100000000000000",  // 1 million of them to the creator
	Mint:          qsdm.QSDM20MintIssuer,
}
out, err := client.SendQSDM20(ctx, create, 0 /* fee in dust */, signer)
if err != nil {
	return err
}
// out.Token is the new token's id; follow out.TxID with GetReceipt.

send := qsdm.QSDM20Action{
	Action: qsdm.QSDM20ActionTransfer,
	Token:  out.Token,
	To:     "1111111111111111111111111111111111111111111111111111111111111111",
	Amount: "150000000", // 1.5 tokens
}
_, err = client.SendQSDM20(ctx, send, 0, signer)
```

| Step | Function | Route |
|---|---|---|
| The sender's account nonce | `GetAccount` (`PrepareQSDM20` calls it) | `GET /api/v1/mining/account?address=` |
| Check the fields offline | `ValidateQSDM20Action` (the node's field rules) | |
| Sign | `SignQSDM20` signs `QSDM20Transaction.SigningDigest()` | |
| Submit | `SubmitQSDM20` | `POST /api/v1/qsdm20/submit` |
| All of the above | `SendQSDM20` | |
| Read | `GetQSDM20Tokens`, `GetQSDM20Token`, `GetQSDM20Holders`, `GetQSDM20Balances` | `GET /api/v1/qsdm20/...` |

Amounts are whole base units as decimal strings, up to 2^256 - 1.
`QSDM20TokenID(creator, nonce)` gives a token's id before its create is
sent. A refusal is an `*ErrAPI`: HTTP 422 for a broken rule, with nothing
charged. With `qsdmcli`, `wallet sign-token-tx` signs offline and
`qsdm20 submit` posts the result.

## Methods and routes

| Method | Route | Public |
|---|---|---|
| `GetBalance` | `GET /api/v1/wallet/balance?address=` | yes |
| `GetWalletNonce` | `GET /api/v1/wallet/nonce?sender=` | yes |
| `GetChainParents`, `GetPoEParents` | `GET /api/v1/chain/parents` | yes |
| `GetReceipt`, `GetReceipts` | `GET /api/v1/receipts/{tx_id}`, `GET /api/v1/receipts` | yes |
| `PrepareTransfer` | the nonce and parents routes above | yes |
| `SubmitSignedTransfer`, `SendTransfer` | `POST /api/v1/wallet/submit-signed` | yes (the signature is the authorization) |
| `GetStreamActionNonce`, `GetStreams`, `GetStream` | `GET /api/v1/streams/...` | yes |
| `SubmitStreamAction` | `POST /api/v1/streams/actions/submit-signed` | yes, but the public pilot answers 503 while stream actions are paused |
| `GetAccount` | `GET /api/v1/mining/account?address=` | yes |
| `PrepareQSDM20`, `SubmitQSDM20`, `SendQSDM20` | `POST /api/v1/qsdm20/submit` | yes (the signature is the authorization); refused until the activation height |
| `GetQSDM20Tokens`, `GetQSDM20Token`, `GetQSDM20Holders`, `GetQSDM20Balances` | `GET /api/v1/qsdm20/tokens`, `/tokens/{id}`, `/tokens/{id}/holders`, `/balances/{address}` | yes |
| `GetLiveness`, `GetReadiness` | `GET /api/v1/health/live`, `/ready` | yes |
| `GetNodeStatus` | `GET /api/v1/status` | yes |
| `SendTransaction` (deprecated) | none: returns `ErrUnsignedSendRemoved` without a request | |
| `GetTransaction` (deprecated) | `GET /api/v1/transactions/{tx_id}` | no: operator JWT (401 otherwise); use `GetReceipt` |
| `GetPeers` (deprecated) | `GET /api/v1/network/peers` | no node registers it (401/404); use `GetNodeStatus().Peers` |
| `GetMetricsJSON`, `GetMetricsPrometheus` (deprecated) | `/api/metrics`, `/api/metrics/prometheus` | operator dashboard only |

`SendTransaction` used to post an unsigned `{from, to, amount}` body to
`POST /api/v1/wallet/send`. That route is operator-only (an operator JWT plus
a request signature made with the node's own key), it spends from the node's
own wallet rather than from `from`, and it expects
`{recipient, amount, fee, geotag, parent_cells}`, so the old call never
worked against a node. `SetAPIKey` sends `X-API-Key`, which QSDM nodes do not
read; `SetToken` sends an operator JWT.

## Tests

```bash
go vet ./... && go test ./...
```

The tests run against local fake nodes. `TestTransferEnvelope_SigningBytesMatchNode`
pins `SigningBytes` to bytes printed by the node's own `pkg/wallet` code.
The `TestQSDM20_*` tests check token ids, signing digests and the field rules
against `testdata/qsdm20_vectors.json`, which the node's own test
(`pkg/chain` `TestQSDM20SDKVectors`) recomputes with the node's code.
