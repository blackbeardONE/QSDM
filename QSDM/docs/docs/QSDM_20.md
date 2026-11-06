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

A QSDM-20 transaction has `contract_id` `qsdm/token/v1`. Its CELL `amount` is 0 and its `recipient`
is empty. The `payload` is a JSON object with an `action` field.

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
- `open` (fair launch): anyone may mint up to `mint_limit` units per transaction, to themselves,
  until `max_supply` units have been minted. `initial_supply` may be 0.

The protocol counts units minted and units burned separately. Burned units are gone: a mint is
allowed only while minted + amount <= `max_supply`, so burning never makes room for new units.

### Field rules

- `symbol`: 1 to 12 characters, `A` to `Z` and `0` to `9`. Symbols are not unique; the token id is.
- `name`: 1 to 48 characters, no control characters.
- `decimals`: 0 to 18. Amounts are always whole base units; wallets divide by 10^decimals to show them.
- `max_supply`, `initial_supply`, `mint_limit`, `amount`: base units as a decimal string without
  leading zeros, from 1 to 2^256 - 1 (`initial_supply` may be `"0"` in `open` and `issuer` mode;
  `mint_limit` is present only in `open` mode).
- `token`: a token id (below). `to`: a QSDM address (64 lowercase hex characters).

### Token id

The id is the lowercase hex SHA-256 of the length-prefixed fields `qsdm/token/v1/id`, the creator's
address and the creating transaction's nonce. It is known before the transaction is sent and is
unique: one address can use each nonce once.

### Signature

The transaction's ML-DSA-87 signature covers the SHA-256 of a length-prefixed encoding of: the domain
`qsdm/token/v1/tx`, the sender, the nonce, the fee in dust, and the action's fields in the order of the
table above. Every field is written as its tag, its byte length and its bytes, so no two different
transactions can encode the same. The public key in the transaction must hash (SHA-256) to the sender's
address.

### When a transaction is refused

A token transaction that breaks any rule (bad field, unknown token, not the issuer, not enough units,
over `max_supply` or `mint_limit`, below the activation height) is refused before it enters a block:
nothing is charged and the nonce does not move.

## State and the state root

The chain keeps, for each token: id, symbol, name, decimals, issuer, mint mode, `mint_limit`,
`max_supply`, minted, burned and the creation height; and for each holder their balance. Supply is
minted minus burned.

The token state has its own root: the SHA-256 of a canonical listing of every token and every non-zero
balance, sorted by token id and address. It joins the block's state root as a `qsdm-20` segment once
the first token exists, the same way CELL Streams and wallet recovery joined it. Blocks before the
first token keep exactly the roots they have today.

## Activation

The producer and every full node read `qsdm20_activation_height` from their configuration. 0 (the
default) means switched off. Token transactions in a block below the activation height are invalid.

## Public API

Reads (no key, any browser origin):

- `GET /api/v1/qsdm20/tokens`: all tokens, newest first, with a cursor.
- `GET /api/v1/qsdm20/tokens/{id}`: one token.
- `GET /api/v1/qsdm20/tokens/{id}/holders`: its holders, largest balance first, with a cursor.
- `GET /api/v1/qsdm20/balances/{address}`: every token an address holds.

Submit:

- `POST /api/v1/qsdm20/submit`: a signed QSDM-20 transaction.

## Not in version 1

Allowances (`approve` and `transferFrom`), token logos or links, and contracts that hold tokens. Each
would be a new version of the contract id, so version 1 tokens keep working unchanged.
