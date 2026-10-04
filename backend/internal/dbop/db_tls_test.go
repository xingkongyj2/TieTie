package dbop

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestMySQLTLSDisabledWithoutCA(t *testing.T) {
	cfg, err := (MySQLConfig{Host: "mysql", Port: 3306, TLSServerName: "unused.example.test"}).withTLS()
	if err != nil {
		t.Fatal(err)
	}
	for _, database := range []string{"", "tietie"} {
		parsed, err := mysql.ParseDSN(cfg.dsn(database))
		if err != nil {
			t.Fatal(err)
		}
		if parsed.TLS != nil || parsed.TLSConfig != "" {
			t.Fatal("CA not configured, but TLS was enabled")
		}
	}
}

func TestMySQLTLSInvalidCAFailsBeforeConnecting(t *testing.T) {
	invalid := filepath.Join(t.TempDir(), "invalid.pem")
	if err := os.WriteFile(invalid, []byte("not a certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, caFile := range []string{filepath.Join(t.TempDir(), "missing.pem"), invalid} {
		_, err := Open(MySQLConfig{Host: "127.0.0.1", Port: 1, Database: "tietie", TLSCAFile: caFile})
		if err == nil || !strings.Contains(err.Error(), "MYSQL_TLS_CA_FILE") {
			t.Fatalf("Open() should reject invalid CA before connecting, got %v", err)
		}
	}
}

func TestMySQLTLSVerifiesServerAndSeparatesRegistrations(t *testing.T) {
	ca, server := mysqlTLSCertificates(t)
	otherCA, _ := mysqlTLSCertificates(t)
	caFile := writeMySQLTLSCA(t, ca)
	otherCAFile := writeMySQLTLSCA(t, otherCA)
	base := MySQLConfig{Host: "38.76.183.142", Port: 3306, TLSCAFile: caFile}
	trusted, err := base.withTLS()
	if err != nil {
		t.Fatal(err)
	}
	wrongName := base
	wrongName.TLSServerName = "other.example.test"
	wrongName, err = wrongName.withTLS()
	if err != nil {
		t.Fatal(err)
	}
	untrusted := base
	untrusted.TLSCAFile = otherCAFile
	untrusted, err = untrusted.withTLS()
	if err != nil {
		t.Fatal(err)
	}
	if trusted.tlsConfigName == wrongName.tlsConfigName || trusted.tlsConfigName == untrusted.tlsConfigName {
		t.Fatal("different CA/hostname configurations shared a driver registration")
	}
	for _, tc := range []struct {
		name    string
		cfg     MySQLConfig
		version uint16
		wantOK  bool
	}{
		{"verified IP SAN", trusted, tls.VersionTLS12, true},
		{"wrong hostname", wrongName, tls.VersionTLS12, false},
		{"untrusted CA", untrusted, tls.VersionTLS12, false},
		{"TLS 1.1 rejected", trusted, tls.VersionTLS11, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := mysql.ParseDSN(tc.cfg.dsn("tietie"))
			if err != nil {
				t.Fatal(err)
			}
			if parsed.TLS == nil || parsed.TLS.InsecureSkipVerify || parsed.TLS.MinVersion != tls.VersionTLS12 || parsed.AllowFallbackToPlaintext {
				t.Fatal("TLS must verify certificates, require TLS 1.2 and disallow plaintext fallback")
			}
			err = mysqlTLSHandshake(parsed.TLS, server, tc.version)
			if (err == nil) != tc.wantOK {
				t.Fatalf("TLS handshake error = %v, want success=%t", err, tc.wantOK)
			}
		})
	}
	// Database creation must use the same verified transport as the app pool.
	creation, err := mysql.ParseDSN(trusted.dsn(""))
	if err != nil || creation.TLS == nil || creation.TLS.ServerName != base.Host {
		t.Fatalf("database creation lost TLS settings: %v", err)
	}
}

func writeMySQLTLSCA(t *testing.T, ca []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, ca, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func mysqlTLSCertificates(t *testing.T) ([]byte, tls.Certificate) {
	t.Helper()
	caPub, caKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test MySQL CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, caPub, caKey)
	if err != nil {
		t.Fatal(err)
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("38.76.183.142")}, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, ca, pub, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func mysqlTLSHandshake(clientConfig *tls.Config, cert tls.Certificate, version uint16) error {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		defer serverConn.Close()
		server := tls.Server(serverConn, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: version, MaxVersion: version})
		_ = server.HandshakeContext(ctx)
	}()
	err := tls.Client(clientConn, clientConfig).HandshakeContext(ctx)
	_ = clientConn.Close()
	<-serverDone
	return err
}
