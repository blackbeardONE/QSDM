package api

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateCA(t *testing.T) {
	caCert, caKey, caPEM, caKeyPEM, err := GenerateCA("TestOrg", 24*time.Hour)
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	if caCert == nil || caKey == nil {
		t.Fatal("CA cert or key is nil")
	}
	if len(caPEM) == 0 || len(caKeyPEM) == 0 {
		t.Fatal("PEM output is empty")
	}
	if !caCert.IsCA {
		t.Error("cert should be marked as CA")
	}
	if caCert.Subject.CommonName != "TestOrg CA" {
		t.Errorf("CN = %q, want 'TestOrg CA'", caCert.Subject.CommonName)
	}
}

func TestGenerateNodeCert(t *testing.T) {
	caCert, caKey, _, _, err := GenerateCA("TestOrg", 24*time.Hour)
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}

	nodePEM, nodeKeyPEM, err := GenerateNodeCert(caCert, caKey, "node-1", []string{"localhost", "127.0.0.1"}, 24*time.Hour)
	if err != nil {
		t.Fatalf("GenerateNodeCert: %v", err)
	}
	if len(nodePEM) == 0 || len(nodeKeyPEM) == 0 {
		t.Fatal("node PEM output is empty")
	}

	// Verify the cert is signed by the CA
	pair, err := tls.X509KeyPair(nodePEM, nodeKeyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}

	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	if leaf.Subject.CommonName != "node-1" {
		t.Errorf("CN = %q, want node-1", leaf.Subject.CommonName)
	}

	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool}); err != nil {
		t.Errorf("node cert doesn't verify against CA: %v", err)
	}
}

func TestGenerateNodeBundle(t *testing.T) {
	bundle, err := GenerateNodeBundle("test-node", []string{"localhost"})
	if err != nil {
		t.Fatalf("GenerateNodeBundle: %v", err)
	}
	if len(bundle.CACertPEM) == 0 {
		t.Error("CA cert PEM empty")
	}
	if len(bundle.NodeCertPEM) == 0 {
		t.Error("Node cert PEM empty")
	}
}

func TestWriteBundleToDisk(t *testing.T) {
	bundle, err := GenerateNodeBundle("disk-node", []string{"localhost"})
	if err != nil {
		t.Fatalf("GenerateNodeBundle: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "certs")
	caCert, nodeCert, nodeKey, err := bundle.WriteBundleToDisk(dir)
	if err != nil {
		t.Fatalf("WriteBundleToDisk: %v", err)
	}

	for _, path := range []string{caCert, nodeCert, nodeKey} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("file %s not found: %v", path, err)
		}
	}
}

func TestMTLSServerClientHandshake(t *testing.T) {
	// Generate CA + two node bundles
	caCert, caKey, caPEM, _, err := GenerateCA("QSDM-Test", 24*time.Hour)
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}

	serverCertPEM, serverKeyPEM, err := GenerateNodeCert(caCert, caKey, "server", []string{"127.0.0.1"}, 24*time.Hour)
	if err != nil {
		t.Fatalf("server cert: %v", err)
	}

	clientCertPEM, clientKeyPEM, err := GenerateNodeCert(caCert, caKey, "client", []string{"127.0.0.1"}, 24*time.Hour)
	if err != nil {
		t.Fatalf("client cert: %v", err)
	}

	// Server TLS config
	serverCert, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
	if err != nil {
		t.Fatalf("server key pair: %v", err)
	}

	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caPEM)

	serverTLS := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
	}

	// Start TLS server
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cn := ""
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			cn = r.TLS.PeerCertificates[0].Subject.CommonName
		}
		w.Write([]byte("hello " + cn))
	})

	srv := httptest.NewUnstartedServer(handler)
	srv.TLS = serverTLS
	srv.StartTLS()
	defer srv.Close()

	// Client TLS config
	clientCert, err := tls.X509KeyPair(clientCertPEM, clientKeyPEM)
	if err != nil {
		t.Fatalf("client key pair: %v", err)
	}

	clientTLS := &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      caPool,
		MinVersion:   tls.VersionTLS13,
	}

	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: clientTLS},
	}

	resp, err := client.Get(srv.URL + "/test")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello client" {
		t.Errorf("response = %q, want 'hello client'", string(body))
	}
}

func TestMTLSRejectsUnauthenticatedClient(t *testing.T) {
	caCert, caKey, caPEM, _, err := GenerateCA("QSDM-Test", 24*time.Hour)
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}

	serverCertPEM, serverKeyPEM, err := GenerateNodeCert(caCert, caKey, "server", []string{"127.0.0.1"}, 24*time.Hour)
	if err != nil {
		t.Fatalf("server cert: %v", err)
	}

	serverCert, _ := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caPEM)

	serverTLS := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
	}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("should not reach here"))
	}))
	srv.TLS = serverTLS
	srv.StartTLS()
	defer srv.Close()

	// Client WITHOUT a certificate
	noAuthTLS := &tls.Config{
		RootCAs:    caPool,
		MinVersion: tls.VersionTLS13,
	}

	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: noAuthTLS},
	}

	_, err = client.Get(srv.URL + "/test")
	if err == nil {
		t.Fatal("expected TLS handshake to fail without client cert, but it succeeded")
	}
}

func TestMTLSRejectsWrongCA(t *testing.T) {
	// Legitimate CA trusted by the server
	caCert, caKey, caPEM, _, _ := GenerateCA("QSDM-Legit", 24*time.Hour)

	serverCertPEM, serverKeyPEM, _ := GenerateNodeCert(caCert, caKey, "server", []string{"127.0.0.1"}, 24*time.Hour)
	serverCert, _ := tls.X509KeyPair(serverCertPEM, serverKeyPEM)

	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caPEM)

	serverTLS := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
	}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("should not reach here"))
	}))
	srv.TLS = serverTLS
	srv.StartTLS()
	defer srv.Close()

	// Separate rogue CA (not trusted by the server)
	rogueCACert, rogueCAKey, rogueCAPEM, _, _ := GenerateCA("Rogue-CA", 24*time.Hour)
	rogueNodePEM, rogueNodeKeyPEM, _ := GenerateNodeCert(rogueCACert, rogueCAKey, "rogue-node", []string{"127.0.0.1"}, 24*time.Hour)
	roguePair, err := tls.X509KeyPair(rogueNodePEM, rogueNodeKeyPEM)
	if err != nil {
		t.Fatalf("rogue key pair: %v", err)
	}

	// Client trusts the rogue CA for server verification, but the server
	// doesn't trust the rogue CA for client verification.
	roguePool := x509.NewCertPool()
	roguePool.AppendCertsFromPEM(rogueCAPEM)
	// Also add legit CA so server cert passes client-side verification.
	roguePool.AppendCertsFromPEM(caPEM)

	clientTLS := &tls.Config{
		Certificates: []tls.Certificate{roguePair},
		RootCAs:      roguePool,
		MinVersion:   tls.VersionTLS13,
	}

	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: clientTLS},
	}

	_, err = client.Get(srv.URL + "/test")
	if err == nil {
		t.Fatal("expected TLS handshake to fail with rogue-signed cert, but it succeeded")
	}
}
