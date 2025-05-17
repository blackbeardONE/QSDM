package storage

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/gocql/gocql"
)

// ScyllaStorage implements storage using ScyllaDB.
type ScyllaStorage struct {
    session *gocql.Session
}

// NewScyllaStorage creates a new ScyllaStorage instance.
func NewScyllaStorage(hosts []string, keyspace string) (*ScyllaStorage, error) {
    cluster := gocql.NewCluster(hosts...)
    cluster.Keyspace = keyspace
    cluster.Consistency = gocql.Quorum
    cluster.Timeout = 10 * time.Second

    session, err := cluster.CreateSession()
    if err != nil {
        return nil, fmt.Errorf("failed to create ScyllaDB session: %w", err)
    }

    return &ScyllaStorage{
        session: session,
    }, nil
}

// StoreTransaction stores a transaction in ScyllaDB.
func (s *ScyllaStorage) StoreTransaction(tx []byte) error {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    query := `INSERT INTO transactions (id, data) VALUES (?, ?)`
    id := gocql.TimeUUID()

    if err := s.session.Query(query, id, tx).WithContext(ctx).Exec(); err != nil {
        log.Printf("Failed to store transaction in ScyllaDB: %v", err)
        return err
    }
    log.Printf("Stored transaction %s in ScyllaDB", id.String())
    return nil
}

// Close closes the ScyllaDB session.
func (s *ScyllaStorage) Close() {
    s.session.Close()
}
