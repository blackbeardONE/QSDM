package storage

import (
	"testing"

	"github.com/gocql/gocql"
)

func TestApplyScyllaClusterOptions_certWithoutKey(t *testing.T) {
	c := gocql.NewCluster("127.0.0.1")
	err := applyScyllaClusterOptions(c, &ScyllaClusterConfig{TLSCertPath: "client.crt"})
	if err == nil {
		t.Fatal("expected error when only cert is set")
	}
}

func TestApplyScyllaClusterOptions_usernameOnly(t *testing.T) {
	c := gocql.NewCluster("127.0.0.1")
	err := applyScyllaClusterOptions(c, &ScyllaClusterConfig{Username: "cassandra", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Authenticator == nil {
		t.Fatal("expected authenticator")
	}
}

func TestEnsureDevKeyspaceIfRequested_invalidKeyspaceName(t *testing.T) {
	t.Setenv("SCYLLA_AUTO_CREATE_KEYSPACE", "1")
	err := ensureDevKeyspaceIfRequested([]string{"127.0.0.1"}, "bad-name", nil)
	if err == nil {
		t.Fatal("expected error for invalid keyspace identifier")
	}
}

func TestScyllaClusterConfigFromEnv_empty(t *testing.T) {
	t.Setenv("SCYLLA_USERNAME", "")
	t.Setenv("SCYLLA_PASSWORD", "")
	t.Setenv("SCYLLA_TLS_CA_PATH", "")
	t.Setenv("SCYLLA_TLS_CERT_PATH", "")
	t.Setenv("SCYLLA_TLS_KEY_PATH", "")
	t.Setenv("SCYLLA_TLS_INSECURE_SKIP_VERIFY", "")
	if ScyllaClusterConfigFromEnv() != nil {
		t.Fatal("expected nil when no env set")
	}
}
