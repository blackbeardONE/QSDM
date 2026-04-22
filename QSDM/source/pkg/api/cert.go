package api

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"time"
)

// generateSelfSignedCert generates a self-signed certificate for development
func (s *Server) generateSelfSignedCert() (string, string, error) {
	certFile := "api_server.crt"
	keyFile := "api_server.key"

	// Check if certificates already exist
	if _, err := os.Stat(certFile); err == nil {
		if _, err := os.Stat(keyFile); err == nil {
			s.logger.Info("Using existing self-signed certificates", "cert", certFile, "key", keyFile)
			return certFile, keyFile, nil
		}
	}

	// Generate private key (4096 bits for maximum security)
	privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate private key: %w", err)
	}

	// Create certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization:  []string{"QSDM"},
			Country:       []string{"US"},
			Province:      []string{""},
			Locality:      []string{""},
			StreetAddress: []string{""},
			PostalCode:    []string{""},
		},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour), // Valid for 1 year
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}

	// Create certificate
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return "", "", fmt.Errorf("failed to create certificate: %w", err)
	}

	// Write certificate file
	certOut, err := os.Create(certFile)
	if err != nil {
		return "", "", fmt.Errorf("failed to open cert file: %w", err)
	}
	defer certOut.Close()

	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		return "", "", fmt.Errorf("failed to write cert: %w", err)
	}

	// Write private key file
	keyOut, err := os.OpenFile(keyFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return "", "", fmt.Errorf("failed to open key file: %w", err)
	}
	defer keyOut.Close()

	privateKeyDER := x509.MarshalPKCS1PrivateKey(privateKey)
	if err := pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: privateKeyDER}); err != nil {
		return "", "", fmt.Errorf("failed to write key: %w", err)
	}

	s.logger.Info("Generated self-signed certificate", "cert", certFile, "key", keyFile)
	return certFile, keyFile, nil
}

