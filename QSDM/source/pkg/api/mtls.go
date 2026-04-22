package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"time"
)

// MTLSConfig holds configuration for mutual TLS.
type MTLSConfig struct {
	// CA certificate used to verify peer node certificates.
	CACertFile string
	CAKeyFile  string

	// Node certificate and key (signed by the CA).
	NodeCertFile string
	NodeKeyFile  string
}

// NodeCertBundle contains the generated CA + node certificate pair.
type NodeCertBundle struct {
	CACertPEM   []byte
	CAKeyPEM    []byte
	NodeCertPEM []byte
	NodeKeyPEM  []byte
}

// GenerateCA creates a self-signed CA certificate and key for mTLS.
func GenerateCA(org string, validFor time.Duration) (*x509.Certificate, *ecdsa.PrivateKey, []byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("generate CA key: %w", err)
	}

	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))

	template := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{org},
			CommonName:   org + " CA",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(validFor),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("create CA cert: %w", err)
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("parse CA cert: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, _ := x509.MarshalECPrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return cert, key, certPEM, keyPEM, nil
}

// GenerateNodeCert creates a certificate signed by the CA for a specific node.
func GenerateNodeCert(caCert *x509.Certificate, caKey *ecdsa.PrivateKey, nodeID string, hosts []string, validFor time.Duration) ([]byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate node key: %w", err)
	}

	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))

	template := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: caCert.Subject.Organization,
			CommonName:   nodeID,
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add(validFor),
		KeyUsage:  x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
			x509.ExtKeyUsageClientAuth,
		},
	}

	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, h)
		}
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("create node cert: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, _ := x509.MarshalECPrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return certPEM, keyPEM, nil
}

// GenerateNodeBundle creates a full CA + node cert pair in one call.
func GenerateNodeBundle(nodeID string, hosts []string) (*NodeCertBundle, error) {
	caCert, caKey, caPEM, caKeyPEM, err := GenerateCA("QSDM", 10*365*24*time.Hour)
	if err != nil {
		return nil, err
	}

	nodePEM, nodeKeyPEM, err := GenerateNodeCert(caCert, caKey, nodeID, hosts, 365*24*time.Hour)
	if err != nil {
		return nil, err
	}

	return &NodeCertBundle{
		CACertPEM:   caPEM,
		CAKeyPEM:    caKeyPEM,
		NodeCertPEM: nodePEM,
		NodeKeyPEM:  nodeKeyPEM,
	}, nil
}

// WriteBundleToDisk saves the cert bundle to the given directory.
func (b *NodeCertBundle) WriteBundleToDisk(dir string) (caCert, nodeCert, nodeKey string, err error) {
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", "", "", fmt.Errorf("create cert dir: %w", err)
	}

	caCert = dir + "/ca.crt"
	nodeCert = dir + "/node.crt"
	nodeKey = dir + "/node.key"

	if err = os.WriteFile(caCert, b.CACertPEM, 0644); err != nil {
		return "", "", "", err
	}
	if err = os.WriteFile(nodeCert, b.NodeCertPEM, 0644); err != nil {
		return "", "", "", err
	}
	if err = os.WriteFile(nodeKey, b.NodeKeyPEM, 0600); err != nil {
		return "", "", "", err
	}
	return caCert, nodeCert, nodeKey, nil
}

// ConfigureMTLS creates a TLS config that requires and verifies client certificates
// signed by the given CA. Both the server and client sides use the same node cert.
func ConfigureMTLS(cfg MTLSConfig) (*tls.Config, error) {
	caCertPEM, err := os.ReadFile(cfg.CACertFile)
	if err != nil {
		return nil, fmt.Errorf("read CA cert: %w", err)
	}

	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caCertPEM) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}

	nodeCert, err := tls.LoadX509KeyPair(cfg.NodeCertFile, cfg.NodeKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load node cert/key: %w", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{nodeCert},
		ClientCAs:    caPool,
		RootCAs:      caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
	}, nil
}

// NewMTLSClient creates an *http.Client that presents a client certificate
// and verifies the server's certificate against the CA pool.
func NewMTLSClient(cfg MTLSConfig) (*tls.Config, error) {
	caCertPEM, err := os.ReadFile(cfg.CACertFile)
	if err != nil {
		return nil, fmt.Errorf("read CA cert: %w", err)
	}

	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caCertPEM) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}

	clientCert, err := tls.LoadX509KeyPair(cfg.NodeCertFile, cfg.NodeKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load client cert/key: %w", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      caPool,
		MinVersion:   tls.VersionTLS13,
	}, nil
}
