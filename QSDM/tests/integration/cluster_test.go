package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestMultiNodeCluster tests a multi-node QSDM cluster
func TestMultiNodeCluster(t *testing.T) {
	// Test endpoints for each node
	nodes := []struct {
		name     string
		dashboard string
		api      string
	}{
		{"node1", "http://localhost:8081", "http://localhost:8080"},
		{"node2", "http://localhost:8082", "http://localhost:8083"},
		{"node3", "http://localhost:8084", "http://localhost:8085"},
	}

	// Test each node
	for _, node := range nodes {
		t.Run(fmt.Sprintf("Node_%s_Health", node.name), func(t *testing.T) {
			testNodeHealth(t, node.dashboard)
		})

		t.Run(fmt.Sprintf("Node_%s_Metrics", node.name), func(t *testing.T) {
			testNodeMetrics(t, node.dashboard)
		})

		t.Run(fmt.Sprintf("Node_%s_API", node.name), func(t *testing.T) {
			testNodeAPI(t, node.api)
		})
	}

	// Test cluster connectivity
	t.Run("Cluster_Connectivity", func(t *testing.T) {
		testClusterConnectivity(t, nodes)
	})
}

// testNodeHealth tests node health endpoint
func testNodeHealth(t *testing.T, baseURL string) {
	url := fmt.Sprintf("%s/api/health", baseURL)
	
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("Failed to connect to %s: %v", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	var health map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("Failed to decode health response: %v", err)
	}

	// Check overall status
	if status, ok := health["overall_status"].(string); ok {
		if status != "healthy" {
			t.Errorf("Expected healthy status, got %s", status)
		}
	}
}

// testNodeMetrics tests node metrics endpoint
func testNodeMetrics(t *testing.T, baseURL string) {
	url := fmt.Sprintf("%s/api/metrics", baseURL)
	
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("Failed to connect to %s: %v", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	var metrics map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&metrics); err != nil {
		t.Fatalf("Failed to decode metrics response: %v", err)
	}

	// Verify metrics structure
	requiredFields := []string{"uptime_seconds", "transactions_valid", "transactions_invalid"}
	for _, field := range requiredFields {
		if _, ok := metrics[field]; !ok {
			t.Errorf("Missing required metric field: %s", field)
		}
	}
}

// testNodeAPI tests node API endpoint
func testNodeAPI(t *testing.T, baseURL string) {
	url := fmt.Sprintf("%s/api/v1/health", baseURL)
	
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("Failed to connect to %s: %v", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}

// testClusterConnectivity tests connectivity between nodes
func testClusterConnectivity(t *testing.T, nodes []struct {
	name     string
	dashboard string
	api      string
}) {
	// Test that all nodes are reachable
	for _, node := range nodes {
		client := &http.Client{Timeout: 5 * time.Second}
		_, err := client.Get(fmt.Sprintf("%s/api/health", node.dashboard))
		if err != nil {
			t.Errorf("Node %s is not reachable: %v", node.name, err)
		}
	}
}

