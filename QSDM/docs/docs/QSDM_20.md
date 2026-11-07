# QSDM-20 tokens (version 1)

Status: specification. The code ships switched off; tokens can be created only after a producer
release sets an activation height. Until then nothing on the chain changes.

QSDM-20 lets anyone create a token on QSDM and send it to any QSDM address. Tokens live in the
chain's state next to CELL balances. Every token action is an ordinary QSDM transaction:

- signed with ML-DSA-87 by the sender's wallet key (the same key that holds the sender's CELL);
- ordered by the sender's account nonce, so it cannot be replayed;
- paying its fee in CELL, which is burned like every other fee.

There is no smart contract to audit for each token: the rules below are part of the protocol and
apply to every token the same way.

## Transactions

A QSDM-20 transaction has `contract_id` `qsdm/token/v1`. Its CELL `amount` is 0, its `recipient`
is empty and its gas limit is 0. The `payload` is a JSON object with an `action` field. The sender
must already have a CELL account: the transaction carries that account's current nonce, and the fee
(which may be 0) is paid from it. Anyone can receive tokens, with or without a CELL account.

| Action | Fields | Who may send it | Effect |
| --- | --- | --- | --- |
| `create` | `symbol`, `name`, `decimals`, `max_supply`, `initial_supply`, `mint`, `mint_limit` | anyone | Creates a token. `initial_supply` goes to the sender, who becomes the token's issuer. |
| `transfer` | `token`, `to`, `amount` | a holder | Moves `amount` from the sender to `to`. |
| `mint` | `token`, `to`, `amount` | the issuer (`mint` = `issuer`) or anyone (`mint` = `open`) | Creates new units, never past `max_supply`. |
| `burn` | `token`, `amount` | a holder | Destroys `amount` of the sender's units. |
| `renounce` | `token` | the issuer | Turns minting off for good; `max_supply` becomes the amount minted so far. |

### Minting modes

- `none`: a fixed supply. `initial_supply` must equal `max_supply`.
- `issuer`: the issuer may mint to any address until `max_supply` units have been minted.
- `open` (fair launch): anyone, the issuer included, may mint up to `mint_limit` units per
  transaction, to themselves (`to` must be the sender), until `max_supply` units have been minted.
  `initial_supply` may be 0.

The protocol counts units minted and units burned separately. Burned units are gone: a mint is
allowed only while minted + amount <= `max_supply`, so burning never makes room for new units.

`renounce` sets the mint mode to `none`, sets `max_supply` to the amount minted so far and drops
`mint_limit`. It is refused when minting is already off (mint mode `none`).

### Field rules

- `symbol`: 1 to 12 characters, `A` to `Z` and `0` to `9`. Symbols are not unique; the token id is.
- `name`: 1 to 48 characters (Unicode code points) of valid UTF-8, none of them a control
  character (Unicode category Cc).
- `decimals`: an integer from 0 to 18. Amounts are always whole base units; wallets divide by
  10^decimals to show them.
- `max_supply`, `initial_supply`, `mint_limit`, `amount`: base units as a decimal string without
  leading zeros, from 1 to 2^256 - 1 (`initial_supply` may be `"0"` in `open` and `issuer` mode;
  `mint_limit` is present only in `open` mode). `initial_supply` is at most `max_supply`.
- `token`: a token id (below). `to`: a QSDM address (64 lowercase hex characters).
- Each action carries exactly the fields of its row in the table above; any other field is refused.

### Payload encoding

The payload is the compact JSON that Go's `encoding/json` writes for the action: keys in the order
`action`, `symbol`, `name`, `decimals`, `max_supply`, `initial_supply`, `mint`, `mint_limit`,
`token`, `to`, `amount`; absent fields left out; no spaces; and `<`, `>` and `&` inside strings
written as `encoding/json` escapes them (a backslash, `u` and four hex digits). A node refuses any
other spelling of the same action, so one action has exactly one payload. The submit API builds the
payload from the fields it receives, so a wallet never has to write it.

### Length-prefixed fields

The token id, the signature and the state root use one encoding of a list of fields. Each field
is written as `|`, its tag, `:`, the byte length of its value in decimal, `:` and the value:

```
T(tag, value) = "|" + tag + ":" + len(value) + ":" + value
```

Numbers are written in decimal without leading zeros. Because every value is preceded by its
length, no two different lists of fields give the same bytes.

### Token id

The id is the lowercase hex SHA-256 of the length-prefixed fields `qsdm/token/v1/id`, the creator's
address and the creating transaction's nonce:

```
T("domain", "qsdm/token/v1/id") T("creator", address) T("nonce", nonce)
```

It is known before the transaction is sent and is unique: one address can use each nonce once.

Example: the creator `abab...ab` (`ab` 32 times) and nonce 7 give

```
|domain:16:qsdm/token/v1/id|creator:64:abababababababababababababababababababababababababababababababab|nonce:1:7
id 78d65edcdf5af01c85401a31e3ffd6a4e19e90c16a62f3a222650ef8497b768a
```

### Signature

The transaction's ML-DSA-87 signature (FIPS 204, empty context) covers the 32-byte SHA-256 of a
length-prefixed encoding of: the domain `qsdm/token/v1/tx`, the sender, the nonce, the fee in dust,
the action and the action's fields in the order of the table above:

```
T("domain", "qsdm/token/v1/tx") T("sender", sender) T("nonce", nonce) T("fee_dust", fee) T("action", action)
```

followed by

| Action | Fields, in this order |
| --- | --- |
| `create` | `symbol`, `name`, `decimals`, `max_supply`, `initial_supply`, `mint`, `mint_limit` (empty when absent) |
| `transfer`, `mint` | `token`, `to`, `amount` |
| `burn` | `token`, `amount` |
| `renounce` | `token` |

Each field's tag is its JSON name. Every field is written as its tag, its byte length and its
bytes, so no two different transactions can encode the same; the action is covered too, so a
transfer and a mint with the same fields have different signatures. The public key in the
transaction must hash (SHA-256) to the sender's address. The public key and the signature are
written in lowercase hex.

The transaction's id is `qsdm20-` followed by the lowercase hex of the signed digest, so whoever
relays a transaction cannot change its id. The transaction's fee field, in CELL like on every QSDM
transaction, must be exactly the fee in dust divided by 10^8.

Example: sender `abab...ab`, nonce 7, a fee of 1500 dust, and a `transfer` of 1000 units of token
`cdcd...cd` to `efef...ef` (each 64 characters) give the digest
`a38b5c1cba8af0190de2c87489608e2a2977d227626ef91117a1a1797db5b648` of

```
|domain:16:qsdm/token/v1/tx|sender:64:abab...ab|nonce:1:7|fee_dust:4:1500|action:8:transfer|token:64:cdcd...cd|to:64:efef...ef|amount:4:1000
```

and the transaction id `qsdm20-a38b5c1cba8af0190de2c87489608e2a2977d227626ef91117a1a1797db5b648`.
The `create` of `GROOT`, name `Groot Coin`, 8 decimals, `max_supply` 1000000, `initial_supply`
250, mint `issuer`, by the same sender at nonce 0 with no fee, signs the digest
`d8edcbaca25b19b26a51c2c40fd51c0712d68b5ca5422289476334015870811d`.

### When a transaction is refused

A token transaction that breaks any rule (bad field, unknown token, not the issuer, not enough units,
over `max_supply` or `mint_limit`, below the activation height) is refused before it enters a block:
nothing is charged and the nonce does not move.

The submit API checks every rule against the current state before the mempool takes a transaction.
One that passes there but is made invalid by an earlier transaction in the same block (for example,
the units were spent first) is left out of that block, likewise with nothing charged. From the
activation height on, a follower refuses a block that carries an invalid token transaction (below it,
see [Activation](#activation)).

## State and the state root

The chain keeps, for each token: id, symbol, name, decimals, issuer, mint mode, `mint_limit`,
`max_supply`, minted, burned and the creation height; and for each holder their balance. Supply is
minted minus burned.

The token state has its own root: the SHA-256 of a canonical listing of every token and every non-zero
balance, sorted by token id and address:

```
T("domain", "qsdm/token/v1/state")
for each token, by id ascending:
  T("token", id) T("symbol", ...) T("name", ...) T("decimals", ...) T("issuer", ...) T("mint", ...)
  T("mint_limit", ... or empty) T("max_supply", ...) T("minted", ...) T("burned", ...) T("created_height", ...)
  for each holder of that token with a non-zero balance, by address ascending:
    T("holder", address) T("balance", units)
```

It joins the block's state root as a `qsdm-20` segment once the first token exists, the same way CELL
Streams and wallet recovery joined it:

```
root = hex(SHA-256("state:" + root + "\nqsdm-20:" + token root))
```

applied after the wallet recovery segment and before the enrollment segment. Blocks before the
first token keep exactly the roots they have today.

## Activation

The producer and every full node read `qsdm20_activation_height` from their configuration
(`[consensus] qsdm20_activation_height`, or the environment variable
`QSDM_QSDM20_ACTIVATION_HEIGHT`). 0 (the default) means switched off. Below the activation height,
and always while it is 0, nodes refuse token transactions at admission, so none enters a block, and
blocks are judged exactly as by a node without QSDM-20: a `qsdm/token/v1` transaction that reaches a
block anyway (for example from a producer release older than QSDM-20) is a plain CELL transfer of its
`amount` to its `recipient`, paying its fee and using its nonce, and never touches token state. That
way adding the code makes no block invalid that is valid today. Every node must use the same value. `GET /api/v1/status` reports it as `consensus_auth.qsdm20_activation_height`, and
`consensus_auth.qsdm20_active` says whether the next block accepts token transactions.

## Public API

Reads (no key, any browser origin):

- `GET /api/v1/qsdm20/tokens`: all tokens, newest first (by creation height, then by id), with a
  cursor.
- `GET /api/v1/qsdm20/tokens/{id}`: one token, with its supply and its number of holders.
- `GET /api/v1/qsdm20/tokens/{id}/holders`: its holders, largest balance first (then by address),
  with a cursor.
- `GET /api/v1/qsdm20/balances/{address}`: every token an address holds.

The two lists take an optional `limit`; without it they return every remaining item. A page that
has more after it ends with `next_cursor`; pass it back as `cursor`. A cursor names the last item of
the page, so the pages that follow do not shift when tokens are created.

Submit:

- `POST /api/v1/qsdm20/submit`: a signed QSDM-20 transaction, as the JSON object `sender`, `nonce`,
  `fee_dust`, `payload` (the action as a JSON object), `public_key` and `signature`. The node builds
  the transaction from these fields (payload, id and fee as above), checks it against its current
  state and puts it in the mempool. The response carries the transaction id (`tx_id`, for
  `GET /api/v1/receipts/{tx_id}`) and the token id (for a create, the id the new token gets). The
  sender's current nonce is the `nonce` of `GET /api/v1/mining/account?address={address}`.

The full request and response shapes are in `openapi.yaml`.

## Wallets and tools

Three tools build, sign and submit token transactions today. They work against any node, and the
node refuses the transactions until the activation height:

- the Go SDK (`QSDM/source/sdk/go`): `SendQSDM20`, or `PrepareQSDM20`, `SignQSDM20` and
  `SubmitQSDM20` one at a time, with any ML-DSA-87 signer (`TransferSigner`), plus
  `GetQSDM20Tokens`, `GetQSDM20Token`, `GetQSDM20Holders` and `GetQSDM20Balances`;
- the JavaScript SDK (`qsdm-sdk` 0.5.0): `sendQSDM20`, `prepareQSDM20`, `submitQSDM20` and the same
  four reads;
- `qsdmcli`: `wallet sign-token-tx` signs with a local keystore and prints the submit body,
  `qsdm20 submit` posts it, and `qsdm20 tokens`, `token`, `holders`, `balances` and `token-id` read.

All three are checked against one set of test vectors,
`QSDM/source/sdk/go/testdata/qsdm20_vectors.json`: token ids, signing encodings, digests and
transaction ids, and payloads a node refuses. A node test computes every value in that file with
the node's own code and fails if the file differs, so a client that matches the vectors signs what
nodes verify. QSDM Hive and the browser wallet do not sign token transactions yet.

## Not in version 1

Allowances (`approve` and `transferFrom`), token logos or links, and contracts that hold tokens. Each
would be a new version of the contract id, so version 1 tokens keep working unchanged.
