# QSDM HTTP API - Military-Grade Security

## Overview

The QSDM HTTP API provides a secure REST interface for wallet and validator operations with **military-grade security** features.

## Security Features

### 1. **TLS 1.3 Encryption**
- **TLS Version**: 1.3 only (most secure)
- **Cipher Suites**: 
  - TLS_AES_256_GCM_SHA384
  - TLS_AES_128_GCM_SHA256
  - TLS_CHACHA20_POLY1305_SHA256
- **Key Exchange**: X25519, CurveP256, CurveP384
- **Certificate**: 4096-bit RSA keys (self-signed for development, CA-signed for production)

### 2. **Quantum-Safe Authentication**
- **JWT Tokens**: Signed with CRYSTALS-Dilithium (quantum-resistant)
- **Token Types**: Access tokens (15 min) and refresh tokens (7 days)
- **Nonce Protection**: Prevents replay attacks
- **Token Expiration**: Automatic expiration and validation

### 3. **Request Signing**
- **Quantum-Safe Signatures**: All POST/PUT/DELETE requests must be signed
- **Timestamp Validation**: 5-minute window to prevent replay
- **Nonce Required**: Unique nonce per request
- **Signature Headers**: `X-Timestamp`, `X-Nonce`, `X-Signature`

### 4. **Rate Limiting**
- **Limit**: 100 requests per minute per client
- **Identification**: By IP address or API key
- **Response**: HTTP 429 with `Retry-After` header
- **Automatic Cleanup**: Expired entries removed periodically

### 5. **Security Headers**
All responses include:
- `Strict-Transport-Security`: Force HTTPS (HSTS)
- `X-Frame-Options`: DENY (prevent clickjacking)
- `X-Content-Type-Options`: nosniff
- `X-XSS-Protection`: 1; mode=block
- `Content-Security-Policy`: Strict CSP
- `Referrer-Policy`: no-referrer
- `Permissions-Policy`: Restrictive permissions

### 6. **Role-Based Access Control (RBAC)**
- **Roles**: `user`, `admin`, `validator`
- **Endpoint Protection**: Role-based route guards
- **Context-Based**: Claims stored in request context

### 7. **Audit Logging**
- **All Requests Logged**: Method, path, IP, user, role
- **Response Logging**: Status code, duration
- **Security Events**: Failed auth, rate limit violations, signature failures

### 8. **Input Validation**
- **Request Validation**: All inputs validated
- **Type Checking**: Strict type validation
- **Sanitization**: XSS and injection prevention

## API Endpoints

### Public Endpoints

#### `GET /api/v1/health`
Health check endpoint (no authentication required).

**Response:**
```json
{
  "status": "healthy",
  "timestamp": 1234567890,
  "version": "1.0.0"
}
```

#### `POST /api/v1/auth/login`
Authenticate and receive tokens.

**Request:**
```json
{
  "address": "wallet_address",
  "password": "password"
}
```

**Response:**
```json
{
  "access_token": "jwt_token_here",
  "refresh_token": "refresh_token_here",
  "expires_in": 900
}
```

#### `POST /api/v1/auth/register`
Register a new user.

**Request:**
```json
{
  "address": "wallet_address",
  "password": "password"
}
```

### Authenticated Endpoints

All authenticated endpoints require:
- **Authorization Header**: `Bearer <access_token>`
- **Request Signing** (for POST/PUT/DELETE):
  - `X-Timestamp`: Unix timestamp
  - `X-Nonce`: Unique nonce
  - `X-Signature`: Quantum-safe signature

#### `POST /api/v1/wallet/create`
Create a new wallet.

**Response:**
```json
{
  "address": "wallet_address",
  "balance": 1000.0
}
```

#### `GET /api/v1/wallet/balance?address=<address>`
Get wallet balance.

**Response:**
```json
{
  "address": "wallet_address",
  "balance": 1000.0
}
```

#### `POST /api/v1/wallet/send`
Send a transaction.

**Request:**
```json
{
  "recipient": "recipient_address",
  "amount": 100.0,
  "fee": 0.001,
  "geotag": "US",
  "parent_cells": ["cell1", "cell2"]
}
```

**Response:**
```json
{
  "transaction_id": "tx_id",
  "status": "pending"
}
```

#### `GET /api/v1/wallet/address`
Get wallet address.

**Response:**
```json
{
  "address": "wallet_address"
}
```

#### `GET /api/v1/transactions?limit=50`
Get recent transactions.

**Response:**
```json
{
  "transactions": [],
  "limit": 50
}
```

#### `GET /api/v1/transactions/<tx_id>`
Get transaction by ID.

#### `POST /api/v1/validator/validate`
Validate a transaction.

**Request:**
```json
{
  "transaction_id": "tx_id",
  "parent_cells": [
    {"id": "cell1", "data": "base64_data"}
  ],
  "data": "base64_data"
}
```

**Response:**
```json
{
  "valid": true,
  "message": "optional message"
}
```

## Request Signing

For POST/PUT/DELETE requests, you must sign the request:

1. **Create payload**: `timestamp:nonce:<request_body>`
2. **Sign with Dilithium**: Sign the payload
3. **Encode signature**: Base64 URL encoding
4. **Add headers**:
   - `X-Timestamp`: Unix timestamp (seconds)
   - `X-Nonce`: Unique nonce (32 bytes, base64)
   - `X-Signature`: Base64-encoded signature

**Example (Go):**
```go
timestamp := time.Now().Unix()
nonce := generateNonce()
payload := fmt.Sprintf("%d:%s:", timestamp, nonce)
payloadBytes := append([]byte(payload), requestBody...)
signature := dilithium.Sign(payloadBytes)
signatureB64 := base64.URLEncoding.EncodeToString(signature)

req.Header.Set("X-Timestamp", strconv.FormatInt(timestamp, 10))
req.Header.Set("X-Nonce", nonce)
req.Header.Set("X-Signature", signatureB64)
```

## Error Responses

All errors follow this format:
```json
{
  "error": "Error Type",
  "message": "Detailed error message",
  "status": 400
}
```

**Common Status Codes:**
- `200`: Success
- `201`: Created
- `400`: Bad Request (invalid input)
- `401`: Unauthorized (invalid/missing token)
- `403`: Forbidden (insufficient permissions)
- `429`: Too Many Requests (rate limited)
- `500`: Internal Server Error

## Configuration

Environment variables:
- `API_PORT`: API server port (default: 8443)
- `ENABLE_TLS`: Enable TLS (default: true)
- `TLS_CERT_FILE`: Path to TLS certificate file
- `TLS_KEY_FILE`: Path to TLS private key file

## Production Deployment

### 1. **Use CA-Signed Certificates**
Replace self-signed certificates with certificates from a trusted CA (Let's Encrypt, etc.).

### 2. **Enable Mutual TLS (mTLS)**
Uncomment `ClientAuth: tls.RequireAndVerifyClientCert` in `pkg/api/server.go` for client certificate authentication.

### 3. **Configure Firewall**
- Allow only HTTPS (port 8443)
- Block HTTP (if `ENABLE_TLS=false` is used)
- Restrict access to trusted IPs if needed

### 4. **Monitor Security Events**
- Review audit logs regularly
- Set up alerts for:
  - Failed authentication attempts
  - Rate limit violations
  - Invalid signatures
  - Unusual access patterns

### 5. **Key Management**
- Store private keys securely (HSM, key vault)
- Rotate keys regularly
- Use separate keys for signing and TLS

## Security Best Practices

1. **Never disable TLS in production**
2. **Use strong passwords** for authentication
3. **Rotate tokens regularly**
4. **Monitor audit logs** for suspicious activity
5. **Keep certificates updated**
6. **Use rate limiting** to prevent DDoS
7. **Validate all inputs** on client and server
8. **Use HTTPS only** (no HTTP in production)

## Testing

### Development Mode
```bash
# HTTP mode (INSECURE - development only)
ENABLE_TLS=false API_PORT=8080 ./qsdmplus.exe
```

### Production Mode
```bash
# HTTPS mode with TLS 1.3
ENABLE_TLS=true API_PORT=8443 TLS_CERT_FILE=/path/to/cert.pem TLS_KEY_FILE=/path/to/key.pem ./qsdmplus.exe
```

## Compliance

This API implementation follows:
- **NIST Cybersecurity Framework**
- **OWASP Top 10** protection
- **PCI DSS** requirements (for payment processing)
- **FIPS 140-2** cryptographic standards (via liboqs)

---

**Note**: This API is designed for military-grade security. All security features are enabled by default. Disable only for development/testing purposes.

