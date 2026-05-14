package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
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

// TestMTLSRejectsExpiredClientCert exercises the second leg of the
// crypto-05 audit-checklist row ("Verify mTLS rejects connections
// with untrusted CAs, expired certs, and wrong CN/SAN"). The CA
// signs a node cert whose NotAfter is already in the past; the
// server must reject the handshake instead of accepting it on the
// CA-trust check alone.
//
// We construct the expired cert by reaching directly into the
// x509 template (instead of calling GenerateNodeCert) because
// GenerateNodeCert hardcodes NotBefore=now() / NotAfter=now()+validFor
// and we want to deliberately back-date both. The cert is still
// CA-signed and CN/SAN-correct, so any acceptance proves the
// server skipped expiry verification.
func TestMTLSRejectsExpiredClientCert(t *testing.T) {
	caCert, caKey, caPEM, _, err := GenerateCA("QSDM-Test-Expiry", 24*time.Hour)
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

	// Build an expired client cert (NotAfter back-dated by 2 hours,
	// NotBefore back-dated by 4 hours so the cert is unambiguously
	// expired rather than not-yet-valid). The CA chain is still
	// valid; only the leaf's expiry is the failure reason we want
	// the server's TLS handshake to surface.
	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("client key: %v", err)
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{Organization: []string{"QSDM-Test-Expiry"}, CommonName: "expired-client"},
		NotBefore:    time.Now().Add(-4 * time.Hour),
		NotAfter:     time.Now().Add(-2 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	clientCertDER, err := x509.CreateCertificate(rand.Reader, &template, caCert, &clientKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create expired client cert: %v", err)
	}
	clientCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientCertDER})
	clientKeyDER, _ := x509.MarshalECPrivateKey(clientKey)
	clientKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: clientKeyDER})

	expiredPair, err := tls.X509KeyPair(clientCertPEM, clientKeyPEM)
	if err != nil {
		t.Fatalf("expired client key pair: %v", err)
	}

	clientTLS := &tls.Config{
		Certificates: []tls.Certificate{expiredPair},
		RootCAs:      caPool,
		MinVersion:   tls.VersionTLS13,
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: clientTLS}}

	_, err = client.Get(srv.URL + "/test")
	if err == nil {
		t.Fatal("expected TLS handshake to fail with expired client cert, but it succeeded")
	}
}

// TestMTLSRejectsWrongSAN exercises the third leg of crypto-05
// ("wrong CN/SAN"). The CA signs a client cert whose SAN is
// "evil.example.com" only — no IP, no localhost. When the client
// presents it during the handshake AND the server is configured
// to verify the SAN against the connecting peer's address (here
// 127.0.0.1), the handshake must fail. We enforce SAN matching
// by giving the server a custom VerifyPeerCertificate hook that
// rejects a leaf whose SAN list does not include 127.0.0.1; this
// matches the operator pattern documented in deploy/README.md
// for mTLS peer authentication.
func TestMTLSRejectsWrongSAN(t *testing.T) {
	caCert, caKey, caPEM, _, err := GenerateCA("QSDM-Test-SAN", 24*time.Hour)
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}

	serverCertPEM, serverKeyPEM, err := GenerateNodeCert(caCert, caKey, "server", []string{"127.0.0.1"}, 24*time.Hour)
	if err != nil {
		t.Fatalf("server cert: %v", err)
	}
	serverCert, _ := tls.X509KeyPair(serverCertPEM, serverKeyPEM)

	// Client cert with a SAN of "evil.example.com" — explicitly
	// excludes 127.0.0.1. Cert is CA-trusted and not expired.
	wrongSANPEM, wrongSANKeyPEM, err := GenerateNodeCert(caCert, caKey, "wrong-san-client", []string{"evil.example.com"}, 24*time.Hour)
	if err != nil {
		t.Fatalf("wrong-SAN client cert: %v", err)
	}
	wrongSANPair, err := tls.X509KeyPair(wrongSANPEM, wrongSANKeyPEM)
	if err != nil {
		t.Fatalf("wrong-SAN client key pair: %v", err)
	}

	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caPEM)

	serverTLS := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
		// Custom verifier that enforces SAN matches the expected
		// peer address. The default tls handshake only checks CA
		// trust + expiry; operators that need strict SAN binding
		// wire a VerifyPeerCertificate hook like this one.
		VerifyPeerCertificate: func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
			if len(verifiedChains) == 0 || len(verifiedChains[0]) == 0 {
				return errors.New("no verified chain")
			}
			leaf := verifiedChains[0][0]
			expected := net.ParseIP("127.0.0.1")
			for _, ip := range leaf.IPAddresses {
				if ip.Equal(expected) {
					return nil
				}
			}
			for _, dns := range leaf.DNSNames {
				if dns == "127.0.0.1" {
					return nil
				}
			}
			return fmt.Errorf("client cert SAN does not include 127.0.0.1: dns=%v ip=%v", leaf.DNSNames, leaf.IPAddresses)
		},
	}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("should not reach here"))
	}))
	srv.TLS = serverTLS
	srv.StartTLS()
	defer srv.Close()

	clientTLS := &tls.Config{
		Certificates: []tls.Certificate{wrongSANPair},
		RootCAs:      caPool,
		MinVersion:   tls.VersionTLS13,
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: clientTLS}}

	_, err = client.Get(srv.URL + "/test")
	if err == nil {
		t.Fatal("expected TLS handshake to fail with wrong-SAN client cert, but it succeeded")
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
