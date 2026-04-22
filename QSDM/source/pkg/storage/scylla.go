package storage

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/errors"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
	"github.com/blackbeardONE/QSDM/pkg/security"
	"github.com/gocql/gocql"
	"github.com/klauspost/compress/zstd"
)

// ScyllaStorage implements storage using ScyllaDB.
type ScyllaStorage struct {
	session  *gocql.Session
	keyspace string
}

// ScyllaClusterConfig configures optional CQL authentication and TLS for the native protocol client.
// Pass nil to NewScyllaStorage for the historical plaintext default (dev clusters).
type ScyllaClusterConfig struct {
	Username string
	Password string
	// TLSCaPath: PEM CA bundle to verify the Scylla/Cassandra server certificate.
	TLSCaPath string
	// TLSCertPath / TLSKeyPath: optional client certificate + key for mutual TLS (both required if either is set).
	TLSCertPath string
	TLSKeyPath string
	// TLSInsecureSkipVerify: dev-only; disables server certificate verification (still uses TLS when other TLS fields are set).
	TLSInsecureSkipVerify bool
}

func applyScyllaClusterOptions(cluster *gocql.ClusterConfig, x *ScyllaClusterConfig) error {
	if x == nil {
		return nil
	}
	if strings.TrimSpace(x.Username) != "" {
		cluster.Authenticator = gocql.PasswordAuthenticator{
			Username: strings.TrimSpace(x.Username),
			Password: x.Password,
		}
	}
	hasCert := strings.TrimSpace(x.TLSCertPath) != ""
	hasKey := strings.TrimSpace(x.TLSKeyPath) != ""
	if hasCert != hasKey {
		return fmt.Errorf("scylla TLS: tls_cert_path and tls_key_path must both be set or both empty")
	}
	tlsOn := strings.TrimSpace(x.TLSCaPath) != "" || hasCert || x.TLSInsecureSkipVerify
	if !tlsOn {
		return nil
	}
	ssl := &gocql.SslOptions{
		CaPath:   strings.TrimSpace(x.TLSCaPath),
		CertPath: strings.TrimSpace(x.TLSCertPath),
		KeyPath:  strings.TrimSpace(x.TLSKeyPath),
	}
	if x.TLSInsecureSkipVerify {
		ssl.Config = &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true,
		}
		ssl.EnableHostVerification = false
	} else {
		ssl.EnableHostVerification = true
	}
	cluster.SslOpts = ssl
	return nil
}

// ScyllaClusterConfigFromAuthTLS builds options from explicit fields (e.g. loaded config).
// Returns nil when no auth or TLS option is set (same emptiness rule as ScyllaClusterConfigFromEnv).
func ScyllaClusterConfigFromAuthTLS(username, password, tlsCaPath, tlsCertPath, tlsKeyPath string, tlsInsecureSkipVerify bool) *ScyllaClusterConfig {
	x := &ScyllaClusterConfig{
		Username:              strings.TrimSpace(username),
		Password:              password,
		TLSCaPath:             strings.TrimSpace(tlsCaPath),
		TLSCertPath:           strings.TrimSpace(tlsCertPath),
		TLSKeyPath:            strings.TrimSpace(tlsKeyPath),
		TLSInsecureSkipVerify: tlsInsecureSkipVerify,
	}
	if x.Username == "" && x.Password == "" && x.TLSCaPath == "" && x.TLSCertPath == "" && x.TLSKeyPath == "" && !x.TLSInsecureSkipVerify {
		return nil
	}
	return x
}

// ScyllaClusterConfigFromEnv builds options from SCYLLA_USERNAME, SCYLLA_PASSWORD, SCYLLA_TLS_CA_PATH,
// SCYLLA_TLS_CERT_PATH, SCYLLA_TLS_KEY_PATH, and SCYLLA_TLS_INSECURE_SKIP_VERIFY (true/1/yes).
// Returns nil when no relevant variables are set.
func ScyllaClusterConfigFromEnv() *ScyllaClusterConfig {
	return ScyllaClusterConfigFromAuthTLS(
		os.Getenv("SCYLLA_USERNAME"),
		os.Getenv("SCYLLA_PASSWORD"),
		os.Getenv("SCYLLA_TLS_CA_PATH"),
		os.Getenv("SCYLLA_TLS_CERT_PATH"),
		os.Getenv("SCYLLA_TLS_KEY_PATH"),
		envTruthy(os.Getenv("SCYLLA_TLS_INSECURE_SKIP_VERIFY")),
	)
}

func envTruthy(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	return s == "1" || s == "true" || s == "yes" || s == "on"
}

// ensureDevKeyspaceIfRequested runs CREATE KEYSPACE IF NOT EXISTS (SimpleStrategy, RF=1) when
// SCYLLA_AUTO_CREATE_KEYSPACE is truthy. Intended for single-node dev/CI only; do not use on
// production clusters where the CQL user must not create keyspaces or where replication topology
// differs. keyspace must be a simple identifier ([a-zA-Z0-9_]+).
func ensureDevKeyspaceIfRequested(hosts []string, keyspace string, extra *ScyllaClusterConfig) error {
	if !envTruthy(os.Getenv("SCYLLA_AUTO_CREATE_KEYSPACE")) {
		return nil
	}
	ks := strings.TrimSpace(keyspace)
	if ks == "" {
		return nil
	}
	for _, r := range ks {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return fmt.Errorf("SCYLLA_AUTO_CREATE_KEYSPACE: invalid keyspace name %q (use [a-zA-Z0-9_]+ only)", keyspace)
		}
	}
	c := gocql.NewCluster(hosts...)
	c.Consistency = gocql.One
	c.Timeout = 30 * time.Second
	c.ConnectTimeout = 30 * time.Second
	c.NumConns = 2
	if err := applyScyllaClusterOptions(c, extra); err != nil {
		return err
	}
	sess, err := c.CreateSession()
	if err != nil {
		return fmt.Errorf("SCYLLA_AUTO_CREATE_KEYSPACE: connect: %w", err)
	}
	defer sess.Close()
	q := fmt.Sprintf("CREATE KEYSPACE IF NOT EXISTS %s WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1}", ks)
	if err := sess.Query(q).Exec(); err != nil {
		return fmt.Errorf("SCYLLA_AUTO_CREATE_KEYSPACE: %w", err)
	}
	return nil
}

// NewScyllaStorage creates a new ScyllaStorage instance. extra may be nil; see ScyllaClusterConfig.
func NewScyllaStorage(hosts []string, keyspace string, extra *ScyllaClusterConfig) (*ScyllaStorage, error) {
	if err := ensureDevKeyspaceIfRequested(hosts, keyspace, extra); err != nil {
		return nil, err
	}
	cluster := gocql.NewCluster(hosts...)
	cluster.Keyspace = keyspace
	cluster.Consistency = gocql.Quorum
	cluster.Timeout = 10 * time.Second
	cluster.ConnectTimeout = 10 * time.Second
	cluster.NumConns = 10 // Connection pool size

	if err := applyScyllaClusterOptions(cluster, extra); err != nil {
		return nil, err
	}

	session, err := cluster.CreateSession()
	if err != nil {
		return nil, fmt.Errorf("failed to create ScyllaDB session: %w", err)
	}

	storage := &ScyllaStorage{
		session:  session,
		keyspace: keyspace,
	}

	// Initialize schema
	if err := storage.initSchema(); err != nil {
		session.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	log.Printf("ScyllaDB storage initialized with keyspace: %s", keyspace)
	return storage, nil
}

// initSchema creates necessary tables in ScyllaDB
func (s *ScyllaStorage) initSchema() error {
	// Create transactions table.
	//
	// NOTE on primary key shape: the natural PK here is just `id` (TimeUUID,
	// unique per write). However, Scylla 6.x (and Cassandra 4.x) strictly
	// enforce the materialized-view rule:
	//
	//   "The MV primary key must contain every column of the base primary key
	//    and AT MOST ONE additional non-primary-key column from the base table."
	//
	// Our per-wallet MVs need to be partitioned by sender/recipient AND
	// clustered by timestamp DESC so GetRecentTransactions can stream the N
	// most recent rows without a full-range scan. With a single-column base PK
	// (just `id`), those MVs would try to promote TWO new non-PK columns
	// (e.g. sender + timestamp) into the MV PK, which Scylla rejects with:
	//
	//   "Cannot include more than one non-primary key column 'timestamp' in
	//    materialized view primary key"
	//
	// Promoting `timestamp` into the base PK as a CLUSTERING column fixes this
	// -- `timestamp` becomes part of the base PK, so each per-wallet MV only
	// adds one truly new non-PK column (sender/recipient/tx_id). Since `id` is
	// a freshly generated TimeUUID per insert, there is exactly one row per
	// (id, timestamp) partition in practice, so the clustering column does
	// not alter read semantics for point-lookups by id.
	createTransactionsTable := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s.transactions (
			id UUID,
			tx_id TEXT,
			data BLOB,
			sender TEXT,
			recipient TEXT,
			amount DOUBLE,
			timestamp TIMESTAMP,
			PRIMARY KEY (id, timestamp)
		) WITH CLUSTERING ORDER BY (timestamp DESC)`, s.keyspace)

	if err := s.session.Query(createTransactionsTable).Exec(); err != nil {
		return fmt.Errorf("failed to create transactions table: %w", err)
	}

	// Create balances table
	createBalancesTable := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s.balances (
			address TEXT PRIMARY KEY,
			balance DOUBLE,
			updated_at TIMESTAMP
		)`, s.keyspace)

	if err := s.session.Query(createBalancesTable).Exec(); err != nil {
		return fmt.Errorf("failed to create balances table: %w", err)
	}

	// LWT-backed claim table for wallet tx_id dedupe (stronger than secondary-index SELECT alone).
	createTxIDClaim := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s.wallet_tx_id_claim (
			tx_id text PRIMARY KEY,
			row_uuid uuid,
			inserted_at timestamp
		)`, s.keyspace)
	if err := s.session.Query(createTxIDClaim).Exec(); err != nil {
		return fmt.Errorf("failed to create wallet_tx_id_claim table: %w", err)
	}

	// Create indexes
	createIndexes := []string{
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS ON %s.transactions (tx_id)", s.keyspace),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS ON %s.transactions (sender)", s.keyspace),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS ON %s.transactions (recipient)", s.keyspace),
	}

	for _, indexQuery := range createIndexes {
		if err := s.session.Query(indexQuery).Exec(); err != nil {
			// Index creation may fail if already exists, log but don't fail
			log.Printf("Warning: Index creation: %v", err)
		}
	}

	// Materialized view: partition by wallet tx_id for efficient GetTransaction
	// (avoids relying only on a secondary index).
	//
	// The MV PK must include every column of the base PK -- which is now
	// (id, timestamp) per the note on createTransactionsTable above -- so
	// `timestamp` is included as a clustering column here too. Logically the
	// access pattern is a point lookup by tx_id (unique, enforced by the
	// wallet_tx_id_claim LWT), so the extra clustering columns do not change
	// observable behavior.
	createTxByTxIDMV := fmt.Sprintf(`
		CREATE MATERIALIZED VIEW IF NOT EXISTS %s.transactions_by_tx_id AS
		SELECT id, tx_id, data, sender, recipient, amount, timestamp
		FROM %s.transactions
		WHERE id IS NOT NULL AND tx_id IS NOT NULL AND timestamp IS NOT NULL
		PRIMARY KEY (tx_id, id, timestamp)`, s.keyspace, s.keyspace)
	if err := s.session.Query(createTxByTxIDMV).Exec(); err != nil {
		return fmt.Errorf("transactions_by_tx_id materialized view: %w", err)
	}

	// Materialized views for wallet-scoped recent history (ORDER BY timestamp DESC per partition).
	createBySenderMV := fmt.Sprintf(`
		CREATE MATERIALIZED VIEW IF NOT EXISTS %s.transactions_by_sender AS
		SELECT id, tx_id, data, sender, recipient, amount, timestamp
		FROM %s.transactions
		WHERE id IS NOT NULL AND sender IS NOT NULL AND timestamp IS NOT NULL
		PRIMARY KEY (sender, timestamp, id)
		WITH CLUSTERING ORDER BY (timestamp DESC, id ASC)`, s.keyspace, s.keyspace)
	if err := s.session.Query(createBySenderMV).Exec(); err != nil {
		return fmt.Errorf("transactions_by_sender materialized view: %w", err)
	}

	createByRecipientMV := fmt.Sprintf(`
		CREATE MATERIALIZED VIEW IF NOT EXISTS %s.transactions_by_recipient AS
		SELECT id, tx_id, data, sender, recipient, amount, timestamp
		FROM %s.transactions
		WHERE id IS NOT NULL AND recipient IS NOT NULL AND timestamp IS NOT NULL
		PRIMARY KEY (recipient, timestamp, id)
		WITH CLUSTERING ORDER BY (timestamp DESC, id ASC)`, s.keyspace, s.keyspace)
	if err := s.session.Query(createByRecipientMV).Exec(); err != nil {
		return fmt.Errorf("transactions_by_recipient materialized view: %w", err)
	}

	return nil
}

// StoreTransaction stores a transaction in ScyllaDB with compression and encryption
func (s *ScyllaStorage) StoreTransaction(data []byte) error {
	return s.storeTransactionWithOptions(data, true, "StoreTransaction")
}

// StoreTransactionMigrate inserts the transaction row (and tx_id LWT claim when applicable) without
// applying balance deltas. Use for SQLite→Scylla bulk history import; copy balances separately afterward.
func (s *ScyllaStorage) StoreTransactionMigrate(data []byte) error {
	return s.storeTransactionWithOptions(data, false, "StoreTransactionMigrate")
}

func (s *ScyllaStorage) storeTransactionWithOptions(data []byte, applyBalance bool, opName string) error {
	start := time.Now()
	defer func() {
		latency := time.Since(start)
		metrics := monitoring.GetMetrics()
		metrics.RecordStorageOperation(opName, latency, nil)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Parse transaction to extract metadata
	var txData map[string]interface{}
	if err := json.Unmarshal(data, &txData); err != nil {
		return errors.NewStorageError(opName, fmt.Errorf("failed to parse transaction: %w", err))
	}

	txID, _ := txData["id"].(string)
	sender, _ := txData["sender"].(string)
	recipient, _ := txData["recipient"].(string)
	amount, _ := txData["amount"].(float64)

	// Idempotent ingest: LWT INSERT IF NOT EXISTS on tx_id claim (then main row insert).
	if txID != "" {
		claimRow := gocql.TimeUUID()
		claimQ := fmt.Sprintf(`
			INSERT INTO %s.wallet_tx_id_claim (tx_id, row_uuid, inserted_at)
			VALUES (?, ?, ?)
			IF NOT EXISTS`, s.keyspace)
		applied, err := s.session.Query(claimQ, txID, claimRow, time.Now()).WithContext(ctx).ScanCAS(nil)
		if err != nil {
			return errors.NewStorageError(opName, fmt.Errorf("tx_id LWT claim: %w", err))
		}
		if !applied {
			log.Printf("%s: skip duplicate tx_id=%s (Scylla LWT)", opName, txID)
			return nil
		}
	}

	// Encrypt data
	key := []byte("0123456789abcdef0123456789abcdef")
	encryptedData, err := security.Encrypt(key, data)
	if err != nil {
		return errors.NewStorageError(opName, fmt.Errorf("failed to encrypt: %w", err))
	}

	// Compress data
	var b bytes.Buffer
	encoder, err := zstd.NewWriter(&b, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
	if err != nil {
		return errors.NewStorageError(opName, fmt.Errorf("failed to create zstd encoder: %w", err))
	}
	_, err = encoder.Write(encryptedData)
	if err != nil {
		encoder.Close()
		return errors.NewStorageError(opName, fmt.Errorf("failed to compress: %w", err))
	}
	encoder.Close()
	compressedData := b.Bytes()

	// Store in ScyllaDB
	id := gocql.TimeUUID()
	query := fmt.Sprintf(`
		INSERT INTO %s.transactions (id, tx_id, data, sender, recipient, amount, timestamp)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, s.keyspace)

	if err := s.session.Query(query, id, txID, compressedData, sender, recipient, amount, time.Now()).WithContext(ctx).Exec(); err != nil {
		return errors.NewStorageError(opName, fmt.Errorf("failed to store transaction: %w", err))
	}

	if applyBalance && sender != "" && recipient != "" && amount > 0 {
		if err := s.UpdateBalance(sender, -amount); err != nil {
			log.Printf("Warning: failed to update sender balance: %v", err)
		}
		if err := s.UpdateBalance(recipient, amount); err != nil {
			log.Printf("Warning: failed to update recipient balance: %v", err)
		}
	}

	return nil
}

// GetBalance returns the balance for an address
func (s *ScyllaStorage) GetBalance(address string) (float64, error) {
	start := time.Now()
	defer func() {
		latency := time.Since(start)
		metrics := monitoring.GetMetrics()
		metrics.RecordStorageOperation("GetBalance", latency, nil)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var balance float64
	query := fmt.Sprintf("SELECT balance FROM %s.balances WHERE address = ?", s.keyspace)

	if err := s.session.Query(query, address).WithContext(ctx).Scan(&balance); err != nil {
		if err == gocql.ErrNotFound {
			return 0.0, nil
		}
		return 0.0, errors.NewStorageError("GetBalance", fmt.Errorf("failed to get balance for address %s: %w", address, err))
	}

	return balance, nil
}

// UpdateBalance updates the balance for an address
func (s *ScyllaStorage) UpdateBalance(address string, amount float64) error {
	start := time.Now()
	defer func() {
		latency := time.Since(start)
		metrics := monitoring.GetMetrics()
		metrics.RecordStorageOperation("UpdateBalance", latency, nil)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Get current balance
	currentBalance, err := s.GetBalance(address)
	if err != nil {
		return err
	}

	newBalance := currentBalance + amount
	query := fmt.Sprintf(`
		INSERT INTO %s.balances (address, balance, updated_at)
		VALUES (?, ?, ?)
		IF NOT EXISTS
	`, s.keyspace)

	applied, err := s.session.Query(query, address, newBalance, time.Now()).WithContext(ctx).ScanCAS(nil)
	if err != nil {
		return errors.NewStorageError("UpdateBalance", fmt.Errorf("failed to update balance: %w", err))
	}

	if !applied {
		// Update existing balance
		updateQuery := fmt.Sprintf(`
			UPDATE %s.balances
			SET balance = balance + ?, updated_at = ?
			WHERE address = ?
		`, s.keyspace)

		if err := s.session.Query(updateQuery, amount, time.Now(), address).WithContext(ctx).Exec(); err != nil {
			return errors.NewStorageError("UpdateBalance", fmt.Errorf("failed to update balance: %w", err))
		}
	}

	return nil
}

// SetBalance sets the balance for an address
func (s *ScyllaStorage) SetBalance(address string, balance float64) error {
	start := time.Now()
	defer func() {
		latency := time.Since(start)
		metrics := monitoring.GetMetrics()
		metrics.RecordStorageOperation("SetBalance", latency, nil)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	query := fmt.Sprintf(`
		INSERT INTO %s.balances (address, balance, updated_at)
		VALUES (?, ?, ?)
	`, s.keyspace)

	if err := s.session.Query(query, address, balance, time.Now()).WithContext(ctx).Exec(); err != nil {
		return errors.NewStorageError("SetBalance", fmt.Errorf("failed to set balance: %w", err))
	}

	return nil
}

// recentTxRow is one row from transactions_by_sender / transactions_by_recipient (or legacy index reads).
type recentTxRow struct {
	id        gocql.UUID
	txID      string
	sender    string
	recipient string
	amount    float64
	ts        time.Time
}

func recentTxRowToMap(r recentTxRow) map[string]interface{} {
	return map[string]interface{}{
		"id":        r.txID,
		"sender":    r.sender,
		"recipient": r.recipient,
		"amount":    r.amount,
		"timestamp": r.ts.Format(time.RFC3339),
	}
}

// mergeRecentTransactionRows merges two streams that are each ordered by timestamp descending (then id).
func mergeRecentTransactionRows(asSender, asRecipient []recentTxRow, limit int) []map[string]interface{} {
	if limit <= 0 {
		return nil
	}
	seen := make(map[string]struct{})
	out := make([]map[string]interface{}, 0, limit)
	i, j := 0, 0
	for len(out) < limit && (i < len(asSender) || j < len(asRecipient)) {
		var pick *recentTxRow
		switch {
		case i >= len(asSender):
			pick = &asRecipient[j]
			j++
		case j >= len(asRecipient):
			pick = &asSender[i]
			i++
		case asSender[i].ts.After(asRecipient[j].ts):
			pick = &asSender[i]
			i++
		case asRecipient[j].ts.After(asSender[i].ts):
			pick = &asRecipient[j]
			j++
		default:
			if asSender[i].id == asRecipient[j].id {
				pick = &asSender[i]
				i++
				j++
			} else if asSender[i].id.String() < asRecipient[j].id.String() {
				pick = &asSender[i]
				i++
			} else {
				pick = &asRecipient[j]
				j++
			}
		}
		key := pick.id.String()
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, recentTxRowToMap(*pick))
	}
	return out
}

// mergeRecentTransactionRowsDedupeSort combines unsorted or overlapping rows, dedupes by row id, sorts by time desc.
func mergeRecentTransactionRowsDedupeSort(rows []recentTxRow, limit int) []map[string]interface{} {
	if limit <= 0 {
		return nil
	}
	byID := make(map[string]recentTxRow, len(rows))
	for _, r := range rows {
		k := r.id.String()
		if prev, ok := byID[k]; !ok || r.ts.After(prev.ts) {
			byID[k] = r
		}
	}
	list := make([]recentTxRow, 0, len(byID))
	for _, r := range byID {
		list = append(list, r)
	}
	sort.Slice(list, func(i, j int) bool {
		if !list[i].ts.Equal(list[j].ts) {
			return list[i].ts.After(list[j].ts)
		}
		return list[i].id.String() < list[j].id.String()
	})
	if len(list) > limit {
		list = list[:limit]
	}
	out := make([]map[string]interface{}, len(list))
	for i := range list {
		out[i] = recentTxRowToMap(list[i])
	}
	return out
}

func (s *ScyllaStorage) fetchRecentFromAddrMV(ctx context.Context, mv, partitionColumn, addr string, limit int) ([]recentTxRow, error) {
	if limit <= 0 {
		return nil, nil
	}
	q := fmt.Sprintf(`
		SELECT id, tx_id, sender, recipient, amount, timestamp
		FROM %s.%s
		WHERE %s = ?
		ORDER BY timestamp DESC, id ASC
		LIMIT ?`, s.keyspace, mv, partitionColumn)
	iter := s.session.Query(q, addr, limit).WithContext(ctx).Iter()
	var rows []recentTxRow
	var id gocql.UUID
	var txID, sender, recipient string
	var amount float64
	var ts time.Time
	for iter.Scan(&id, &txID, &sender, &recipient, &amount, &ts) {
		rows = append(rows, recentTxRow{id: id, txID: txID, sender: sender, recipient: recipient, amount: amount, ts: ts})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *ScyllaStorage) fetchRecentLegacyByColumn(ctx context.Context, column, addr string, cap int) ([]recentTxRow, error) {
	q := fmt.Sprintf(`
		SELECT id, tx_id, sender, recipient, amount, timestamp
		FROM %s.transactions
		WHERE %s = ?
		LIMIT ?`, s.keyspace, column)
	iter := s.session.Query(q, addr, cap).WithContext(ctx).Iter()
	var rows []recentTxRow
	var id gocql.UUID
	var txID, sender, recipient string
	var amount float64
	var ts time.Time
	for iter.Scan(&id, &txID, &sender, &recipient, &amount, &ts) {
		rows = append(rows, recentTxRow{id: id, txID: txID, sender: sender, recipient: recipient, amount: amount, ts: ts})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return rows, nil
}

// GetRecentTransactions retrieves recent transactions for an address
func (s *ScyllaStorage) GetRecentTransactions(address string, limit int) ([]map[string]interface{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if limit <= 0 {
		limit = 10
	}

	asSender, errSender := s.fetchRecentFromAddrMV(ctx, "transactions_by_sender", "sender", address, limit)
	asRecipient, errRecipient := s.fetchRecentFromAddrMV(ctx, "transactions_by_recipient", "recipient", address, limit)

	if errSender == nil && errRecipient == nil {
		return mergeRecentTransactionRows(asSender, asRecipient, limit), nil
	}

	if errSender != nil {
		log.Printf("GetRecentTransactions: transactions_by_sender read: %v", errSender)
	}
	if errRecipient != nil {
		log.Printf("GetRecentTransactions: transactions_by_recipient read: %v", errRecipient)
	}

	legacyCap := limit * 100
	if legacyCap < 500 {
		legacyCap = 500
	}
	if legacyCap > 5000 {
		legacyCap = 5000
	}

	var senderRows []recentTxRow
	if errSender == nil {
		senderRows = asSender
	} else {
		rs, errLeg := s.fetchRecentLegacyByColumn(ctx, "sender", address, legacyCap)
		if errLeg != nil {
			log.Printf("GetRecentTransactions: legacy sender index read: %v", errLeg)
		} else {
			senderRows = rs
		}
	}

	var recipientRows []recentTxRow
	if errRecipient == nil {
		recipientRows = asRecipient
	} else {
		rr, errLeg := s.fetchRecentLegacyByColumn(ctx, "recipient", address, legacyCap)
		if errLeg != nil {
			log.Printf("GetRecentTransactions: legacy recipient index read: %v", errLeg)
		} else {
			recipientRows = rr
		}
	}

	if len(senderRows) == 0 && len(recipientRows) == 0 {
		return nil, errors.NewStorageError("GetRecentTransactions", fmt.Errorf("no rows: sender MV err=%v, recipient MV err=%v", errSender, errRecipient))
	}

	combined := append(append([]recentTxRow(nil), senderRows...), recipientRows...)
	return mergeRecentTransactionRowsDedupeSort(combined, limit), nil
}

func (s *ScyllaStorage) scanTransactionRowByWalletTxID(ctx context.Context, txID string) (data []byte, sender, recipient string, amount float64, timestamp time.Time, err error) {
	mv := fmt.Sprintf(`
		SELECT data, sender, recipient, amount, timestamp
		FROM %s.transactions_by_tx_id
		WHERE tx_id = ?
		LIMIT 1`, s.keyspace)
	err = s.session.Query(mv, txID).WithContext(ctx).Scan(&data, &sender, &recipient, &amount, &timestamp)
	if err == nil || err == gocql.ErrNotFound {
		return data, sender, recipient, amount, timestamp, err
	}
	// Older keyspaces may lack the MV; fall back to secondary index on the base table.
	legacy := fmt.Sprintf(`
		SELECT data, sender, recipient, amount, timestamp
		FROM %s.transactions
		WHERE tx_id = ?
		LIMIT 1`, s.keyspace)
	err = s.session.Query(legacy, txID).WithContext(ctx).Scan(&data, &sender, &recipient, &amount, &timestamp)
	return data, sender, recipient, amount, timestamp, err
}

// GetTransaction retrieves a transaction by ID
func (s *ScyllaStorage) GetTransaction(txID string) (map[string]interface{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	data, sender, recipient, amount, timestamp, err := s.scanTransactionRowByWalletTxID(ctx, txID)
	if err != nil {
		if err == gocql.ErrNotFound {
			return nil, errors.NewStorageError("GetTransaction", fmt.Errorf("transaction not found: %s", txID))
		}
		return nil, errors.NewStorageError("GetTransaction", fmt.Errorf("failed to get transaction: %w", err))
	}

	// Decompress
	decoder, err := zstd.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, errors.NewStorageError("GetTransaction", fmt.Errorf("failed to create zstd decoder: %w", err))
	}
	defer decoder.Close()

	encryptedData, err := decoder.DecodeAll(data, nil)
	if err != nil {
		return nil, errors.NewStorageError("GetTransaction", fmt.Errorf("failed to decompress: %w", err))
	}

	// Decrypt
	key := []byte("0123456789abcdef0123456789abcdef")
	decryptedData, err := security.Decrypt(key, encryptedData)
	if err != nil {
		return nil, errors.NewStorageError("GetTransaction", fmt.Errorf("failed to decrypt: %w", err))
	}

	// Parse JSON
	var tx map[string]interface{}
	if err := json.Unmarshal(decryptedData, &tx); err != nil {
		return nil, errors.NewStorageError("GetTransaction", fmt.Errorf("failed to parse transaction: %w", err))
	}

	// Add metadata
	tx["sender"] = sender
	tx["recipient"] = recipient
	tx["amount"] = amount
	tx["timestamp"] = timestamp.Format(time.RFC3339)

	return tx, nil
}

// Ready runs a lightweight cluster query.
func (s *ScyllaStorage) Ready() error {
	if s.session == nil {
		return fmt.Errorf("scylla session not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var v string
	if err := s.session.Query("SELECT release_version FROM system.local").WithContext(ctx).Scan(&v); err != nil {
		return fmt.Errorf("scylla ping: %w", err)
	}
	return nil
}

// Close closes the ScyllaDB session.
func (s *ScyllaStorage) Close() error {
	if s.session != nil {
		s.session.Close()
	}
	return nil
}
