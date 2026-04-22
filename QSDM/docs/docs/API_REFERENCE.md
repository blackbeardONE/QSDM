# QSDM API Reference

## Overview

The QSDM API provides RESTful endpoints for interacting with QSDM nodes. All endpoints require authentication via JWT tokens or API keys.

**Base URL:** `http://localhost:8080` (default)

---

## Authentication

### JWT Token Authentication

Include the JWT token in the `Authorization` header:

```
Authorization: Bearer <token>
```

### API Key Authentication

Include the API key in the `X-API-Key` header:

```
X-API-Key: <api_key>
```

---

## Endpoints

### Wallet Operations

#### Get Balance

**GET** `/api/v1/wallet/balance?address=<address>`

Retrieves the balance for a given address.

**Parameters:**
- `address` (query, required): The wallet address

**Response:**
```json
{
  "balance": 1000.0,
  "address": "address123"
}
```

#### Send Transaction

**POST** `/api/v1/wallet/send`

Sends a transaction from one address to another.

**Request Body:**
```json
{
  "from": "sender_address",
  "to": "recipient_address",
  "amount": 100.0
}
```

**Response:**
```json
{
  "transaction_id": "tx_abc123",
  "status": "pending"
}
```

#### Get Recent Transactions

**GET** `/api/v1/wallet/transactions?address=<address>&limit=<limit>`

Retrieves recent transactions for an address.

**Parameters:**
- `address` (query, required): The wallet address
- `limit` (query, optional): Maximum number of transactions (default: 10)

**Response:**
```json
{
  "transactions": [
    {
      "id": "tx_abc123",
      "sender": "sender_address",
      "recipient": "recipient_address",
      "amount": 100.0,
      "timestamp": "2025-01-01T00:00:00Z"
    }
  ]
}
```

### Transaction Operations

#### Get Transaction

**GET** `/api/v1/transaction/<tx_id>`

Retrieves a transaction by ID.

**Response:**
```json
{
  "id": "tx_abc123",
  "sender": "sender_address",
  "recipient": "recipient_address",
  "amount": 100.0,
  "timestamp": "2025-01-01T00:00:00Z",
  "status": "confirmed"
}
```

### Monitoring

#### Get Metrics

**GET** `/api/metrics`

Retrieves system metrics.

**Response:**
```json
{
  "transactions_processed": 1000,
  "transactions_valid": 950,
  "transactions_invalid": 50,
  "network_messages_sent": 5000,
  "network_messages_received": 4800,
  "uptime_seconds": 3600
}
```

#### Get Health Status

**GET** `/api/health`

Retrieves health status of the node.

**Response:**
```json
{
  "overall_status": "healthy",
  "components": {
    "network": {
      "status": "healthy",
      "message": "Network running normally"
    },
    "storage": {
      "status": "healthy",
      "message": "Storage operational"
    }
  }
}
```

#### Get Network Topology

**GET** `/api/topology`

Retrieves network topology information.

**Response:**
```json
{
  "nodes": [
    {
      "id": "peer_id",
      "label": "Peer Name",
      "type": "peer"
    }
  ],
  "edges": [
    {
      "from": "self_id",
      "to": "peer_id",
      "status": "connected"
    }
  ],
  "peerCount": 5,
  "connectedCount": 3
}
```

### Authentication

#### Login

**POST** `/api/v1/auth/login`

Authenticates a user and returns JWT tokens.

**Request Body:**
```json
{
  "address": "user_address",
  "password": "user_password"
}
```

**Response:**
```json
{
  "access_token": "jwt_access_token",
  "refresh_token": "jwt_refresh_token",
  "csrf_token": "csrf_token"
}
```

---

## Error Responses

All endpoints may return error responses in the following format:

```json
{
  "error": "error_code",
  "message": "Human-readable error message",
  "status": 400
}
```

### Common Error Codes

- `400 Bad Request`: Invalid request parameters
- `401 Unauthorized`: Authentication required or invalid
- `403 Forbidden`: Insufficient permissions
- `404 Not Found`: Resource not found
- `429 Too Many Requests`: Rate limit exceeded
- `500 Internal Server Error`: Server error

---

## Rate Limiting

API endpoints are rate-limited to prevent abuse:

- **Default:** 100 requests per minute per client
- **Login:** 5 requests per minute
- **Registration:** 3 requests per minute
- **Transactions:** 10 requests per minute
- **Dashboard:** 50 requests per minute

Rate limit information is included in response headers:

```
X-RateLimit-Limit: 100
X-RateLimit-Remaining: 95
X-RateLimit-Reset: 1609459200
```

---

## SDKs

### Go SDK

```go
import "github.com/blackbeardONE/QSDM/sdk/go"

client := qsdm.NewClient("http://localhost:8080")
client.SetToken("your_jwt_token")

balance, err := client.GetBalance("address123")
```

### JavaScript SDK

```javascript
import QSDMClient from '@qsdm/sdk';

const client = new QSDMClient('http://localhost:8080');
client.setToken('your_jwt_token');

const balance = await client.getBalance('address123');
```

---

## WebSocket API

WebSocket support for real-time updates is planned for future releases.

---

## Versioning

The API is versioned using the URL path. Current version: `v1`

---

## Support

For API support and questions, please refer to the main QSDM documentation or open an issue on GitHub.
