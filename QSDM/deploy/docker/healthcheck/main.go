// Command qsdm-healthcheck is the container HEALTHCHECK for images without a shell or wget (the distroless miner
// image): it GETs the node's liveness endpoint on the loopback interface and exits 0 on HTTP 200, 1 otherwise.
//
// Usage: qsdm-healthcheck [url]   (default http://127.0.0.1:8080/api/v1/health/live)
package main

import (
	"net/http"
	"os"
	"time"
)

func main() {
	url := "http://127.0.0.1:8080/api/v1/health/live"
	if len(os.Args) > 1 {
		url = os.Args[1]
	}
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		os.Exit(1)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		os.Exit(1)
	}
}
