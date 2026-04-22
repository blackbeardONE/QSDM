package storage

import (
	"database/sql"
	"fmt"
	"log"
	"time"
)

// QueryOptimizer provides query optimization utilities
type QueryOptimizer struct {
	db *sql.DB
}

// NewQueryOptimizer creates a new query optimizer
func NewQueryOptimizer(db *sql.DB) *QueryOptimizer {
	return &QueryOptimizer{db: db}
}

// OptimizeIndexes creates optimized indexes for common queries
func (qo *QueryOptimizer) OptimizeIndexes() error {
	indexes := []struct {
		name  string
		query string
	}{
		{
			name:  "idx_transactions_timestamp",
			query: "CREATE INDEX IF NOT EXISTS idx_transactions_timestamp ON transactions(timestamp DESC);",
		},
		{
			name:  "idx_transactions_sender_timestamp",
			query: "CREATE INDEX IF NOT EXISTS idx_transactions_sender_timestamp ON transactions(sender, timestamp DESC);",
		},
		{
			name:  "idx_transactions_recipient_timestamp",
			query: "CREATE INDEX IF NOT EXISTS idx_transactions_recipient_timestamp ON transactions(recipient, timestamp DESC);",
		},
		{
			name:  "idx_balances_updated_at",
			query: "CREATE INDEX IF NOT EXISTS idx_balances_updated_at ON balances(updated_at DESC);",
		},
	}

	for _, idx := range indexes {
		start := time.Now()
		_, err := qo.db.Exec(idx.query)
		if err != nil {
			log.Printf("Warning: Failed to create index %s: %v", idx.name, err)
			continue
		}
		log.Printf("Created index %s in %v", idx.name, time.Since(start))
	}

	return nil
}

// AnalyzeTables runs ANALYZE on tables to update query planner statistics
func (qo *QueryOptimizer) AnalyzeTables() error {
	tables := []string{"transactions", "balances"}

	for _, table := range tables {
		query := fmt.Sprintf("ANALYZE %s;", table)
		_, err := qo.db.Exec(query)
		if err != nil {
			log.Printf("Warning: Failed to analyze table %s: %v", table, err)
			continue
		}
		log.Printf("Analyzed table %s", table)
	}

	return nil
}

// VacuumDatabase runs VACUUM to reclaim storage and optimize database
func (qo *QueryOptimizer) VacuumDatabase() error {
	log.Println("Running VACUUM (this may take a while)...")
	start := time.Now()
	
	_, err := qo.db.Exec("VACUUM;")
	if err != nil {
		return fmt.Errorf("failed to vacuum database: %w", err)
	}
	
	log.Printf("VACUUM completed in %v", time.Since(start))
	return nil
}

// GetQueryPlan returns the query plan for a given query (for debugging)
func (qo *QueryOptimizer) GetQueryPlan(query string) (string, error) {
	explainQuery := fmt.Sprintf("EXPLAIN QUERY PLAN %s", query)
	
	rows, err := qo.db.Query(explainQuery)
	if err != nil {
		return "", fmt.Errorf("failed to get query plan: %w", err)
	}
	defer rows.Close()

	var plan string
	for rows.Next() {
		var detail, table, index, info string
		if err := rows.Scan(&detail, &table, &index, &info); err != nil {
			continue
		}
		plan += fmt.Sprintf("%s | %s | %s | %s\n", detail, table, index, info)
	}

	return plan, nil
}

// OptimizeConnectionPool optimizes database connection pool settings
func (qo *QueryOptimizer) OptimizeConnectionPool(maxOpen, maxIdle int, maxLifetime time.Duration) {
	qo.db.SetMaxOpenConns(maxOpen)
	qo.db.SetMaxIdleConns(maxIdle)
	qo.db.SetConnMaxLifetime(maxLifetime)
	
	log.Printf("Connection pool optimized: MaxOpen=%d, MaxIdle=%d, MaxLifetime=%v",
		maxOpen, maxIdle, maxLifetime)
}

