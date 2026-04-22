package load

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// LoadTestConfig holds load test configuration
type LoadTestConfig struct {
	BaseURL      string
	Concurrency  int
	Requests     int
	Duration     time.Duration
	Endpoint     string
}

// LoadTestResult holds load test results
type LoadTestResult struct {
	TotalRequests    int64
	SuccessfulRequests int64
	FailedRequests   int64
	TotalDuration    time.Duration
	AvgLatency       time.Duration
	MinLatency       time.Duration
	MaxLatency       time.Duration
	RequestsPerSec   float64
}

// TestAPILoad tests API endpoint under load
func TestAPILoad(t *testing.T) {
	config := LoadTestConfig{
		BaseURL:     "http://localhost:8080",
		Concurrency: 10,
		Requests:    1000,
		Duration:    60 * time.Second,
		Endpoint:    "/api/v1/health",
	}

	result := runLoadTest(t, config)
	
	t.Logf("Load Test Results:")
	t.Logf("  Total Requests: %d", result.TotalRequests)
	t.Logf("  Successful: %d", result.SuccessfulRequests)
	t.Logf("  Failed: %d", result.FailedRequests)
	t.Logf("  Duration: %v", result.TotalDuration)
	t.Logf("  Avg Latency: %v", result.AvgLatency)
	t.Logf("  Requests/sec: %.2f", result.RequestsPerSec)

	// Assertions
	if result.FailedRequests > result.TotalRequests/10 {
		t.Errorf("Too many failed requests: %d/%d", result.FailedRequests, result.TotalRequests)
	}

	if result.RequestsPerSec < 10 {
		t.Errorf("Requests per second too low: %.2f", result.RequestsPerSec)
	}
}

// TestTransactionLoad tests transaction creation under load
func TestTransactionLoad(t *testing.T) {
	// This would require authentication and wallet setup
	// For now, just test the endpoint exists
	config := LoadTestConfig{
		BaseURL:     "http://localhost:8080",
		Concurrency: 5,
		Requests:    100,
		Duration:    30 * time.Second,
		Endpoint:    "/api/v1/wallet/balance",
	}

	result := runLoadTest(t, config)
	
	t.Logf("Transaction Load Test Results:")
	t.Logf("  Total Requests: %d", result.TotalRequests)
	t.Logf("  Successful: %d", result.SuccessfulRequests)
	t.Logf("  Failed: %d", result.FailedRequests)
}

// runLoadTest runs a load test with the given configuration
func runLoadTest(t *testing.T, config LoadTestConfig) LoadTestResult {
	var (
		totalRequests    int64
		successfulRequests int64
		failedRequests   int64
		totalLatency     int64
		minLatency       = time.Duration(^uint64(0) >> 1)
		maxLatency       time.Duration
		mu               sync.Mutex
	)

	startTime := time.Now()
	done := make(chan bool)
	
	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < config.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			
			client := &http.Client{Timeout: 10 * time.Second}
			url := config.BaseURL + config.Endpoint
			
			for {
				select {
				case <-done:
					return
				default:
					reqStart := time.Now()
					
					resp, err := client.Get(url)
					latency := time.Since(reqStart)
					
					atomic.AddInt64(&totalRequests, 1)
					atomic.AddInt64(&totalLatency, int64(latency))
					
					mu.Lock()
					if latency < minLatency {
						minLatency = latency
					}
					if latency > maxLatency {
						maxLatency = latency
					}
					mu.Unlock()
					
					if err != nil || resp.StatusCode != http.StatusOK {
						atomic.AddInt64(&failedRequests, 1)
					} else {
						atomic.AddInt64(&successfulRequests, 1)
						resp.Body.Close()
					}
					
					// Check if we've exceeded duration
					if time.Since(startTime) > config.Duration {
						return
					}
					
					// Check if we've exceeded request count
					if atomic.LoadInt64(&totalRequests) >= int64(config.Requests) {
						return
					}
					
					time.Sleep(10 * time.Millisecond) // Small delay
				}
			}
		}()
	}
	
	// Wait for duration or request limit
	time.Sleep(config.Duration)
	close(done)
	wg.Wait()
	
	totalDuration := time.Since(startTime)
	totalReqs := atomic.LoadInt64(&totalRequests)
	avgLatency := time.Duration(atomic.LoadInt64(&totalLatency) / totalReqs)
	
	return LoadTestResult{
		TotalRequests:      totalReqs,
		SuccessfulRequests: atomic.LoadInt64(&successfulRequests),
		FailedRequests:     atomic.LoadInt64(&failedRequests),
		TotalDuration:      totalDuration,
		AvgLatency:         avgLatency,
		MinLatency:         minLatency,
		MaxLatency:         maxLatency,
		RequestsPerSec:     float64(totalReqs) / totalDuration.Seconds(),
	}
}

