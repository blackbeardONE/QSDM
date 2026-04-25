package main

import (
	"fmt"
	"log"

	"github.com/blackbeardONE/QSDM/internal/dashboard"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

func main() {
	fmt.Println("Starting QSDM Dashboard Test Server...")
	fmt.Println("This will test if the dashboard works independently")
	fmt.Println()

	// Initialize monitoring
	metrics := monitoring.GetMetrics()
	healthChecker := monitoring.NewHealthChecker(metrics)

	// Register some test components
	healthChecker.RegisterComponent("network")
	healthChecker.RegisterComponent("storage")
	healthChecker.RegisterComponent("test")

	// Update some test metrics
	metrics.IncrementTransactionsProcessed()
	metrics.IncrementTransactionsValid()
	metrics.IncrementNetworkMessagesSent()

	// Create and start dashboard
	dash := dashboard.NewDashboard(metrics, healthChecker, "8081", false, dashboard.DashboardNvidiaLock{}, "", "", false, "http://127.0.0.1:8080", nil)

	fmt.Println("Dashboard starting on http://localhost:8081")
	fmt.Println("Open your browser and navigate to: http://localhost:8081")
	fmt.Println("Press Ctrl+C to stop")
	fmt.Println()

	if err := dash.Start(); err != nil {
		log.Fatalf("Failed to start dashboard: %v", err)
	}
}

