package tlsconfig

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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wandxy/morph/internal/config"
	"google.golang.org/grpc/credentials"
)

func TestCredentials_DisabledUsesPlaintextClientOnly(t *testing.T) {
	serverCredentials, err := ServerCredentials(config.AuthTLSConfig{Mode: config.AuthTLSDisabled})
	require.NoError(t, err)
	require.Nil(t, serverCredentials)

	clientCredentials, thumbprint, err := ClientCredentials(
		config.AuthTLSConfig{Mode: config.AuthTLSDisabled},
		"127.0.0.1:50051",
	)
	require.NoError(t, err)
	require.NotNil(t, clientCredentials)
	require.Empty(t, thumbprint)

	_, _, err = ClientCredentials(
		config.AuthTLSConfig{Mode: config.AuthTLSDisabled},
		"rpc.example.com:50051",
	)
	require.EqualError(t, err, "RPC TLS is required for non-loopback addresses")
}

func TestCredentials_LoadServerAndMutualTLSCertificates(t *testing.T) {
	files := writeTestCertificates(t)
	serverCredentials, err := ServerCredentials(config.AuthTLSConfig{
		Mode: config.AuthTLSMutual, ServerCertificate: files.serverCertificate,
		ServerKey: files.serverKey, ClientCA: files.ca,
		MinimumVersion: "1.3",
	})
	require.NoError(t, err)
	require.NotNil(t, serverCredentials)

	clientCredentials, thumbprint, err := ClientCredentials(config.AuthTLSConfig{
		Mode: config.AuthTLSMutual, ServerCA: files.ca,
		ClientCertificate: files.clientCertificate, ClientKey: files.clientKey,
		ServerName: "localhost", MinimumVersion: "1.3",
	}, "127.0.0.1:50051")
	require.NoError(t, err)
	require.NotNil(t, clientCredentials)
	require.NotEmpty(t, thumbprint)
}

func TestCredentials_RejectsIncompleteMutualTLS(t *testing.T) {
	_, err := ServerCredentials(config.AuthTLSConfig{
		Mode: config.AuthTLSMutual,
	})
	require.ErrorContains(t, err, "load RPC server certificate")

	_, _, err = ClientCredentials(config.AuthTLSConfig{
		Mode: config.AuthTLSMutual,
	}, "127.0.0.1:50051")
	require.EqualError(t, err, "RPC mutual TLS client certificate and key are required")
}

func TestCredentials_MutualTLSHandshakeRejectsMissingClientCertificate(t *testing.T) {
	files := writeTestCertificates(t)
	serverCredentials, err := ServerCredentials(config.AuthTLSConfig{
		Mode: config.AuthTLSMutual, ServerCertificate: files.serverCertificate,
		ServerKey: files.serverKey, ClientCA: files.ca,
		MinimumVersion: "1.3",
	})
	require.NoError(t, err)
	caBody, err := os.ReadFile(files.ca)
	require.NoError(t, err)
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(caBody))
	clientCredentials := credentials.NewTLS(&tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    roots,
		ServerName: "localhost",
	})
	serverConnection, clientConnection := net.Pipe()
	require.NoError(t, serverConnection.SetDeadline(time.Now().Add(5*time.Second)))
	require.NoError(t, clientConnection.SetDeadline(time.Now().Add(5*time.Second)))
	serverResult := make(chan error, 1)
	go func() {
		_, _, handshakeErr := serverCredentials.ServerHandshake(serverConnection)
		serverResult <- handshakeErr
	}()

	_, _, _ = clientCredentials.ClientHandshake(
		context.Background(), "localhost", clientConnection,
	)
	require.NoError(t, clientConnection.Close())
	serverErr := <-serverResult
	require.Error(t, serverErr)
}

type testCertificateFiles struct {
	ca                string
	serverCertificate string
	serverKey         string
	clientCertificate string
	clientKey         string
}

func writeTestCertificates(t *testing.T) testCertificateFiles {
	t.Helper()
	dir := t.TempDir()
	caPublic, caPrivate, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	now := time.Now().UTC()
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "Morph test CA"},
		NotBefore:    now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caPublic, caPrivate)
	require.NoError(t, err)
	ca, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)
	caPath := filepath.Join(dir, "ca.pem")
	writePEM(t, caPath, "CERTIFICATE", caDER)

	serverCertificate, serverKey := writeSignedCertificate(
		t, dir, "server", ca, caPrivate,
		[]x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, []string{"localhost"},
	)
	clientCertificate, clientKey := writeSignedCertificate(
		t, dir, "client", ca, caPrivate,
		[]x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, nil,
	)

	return testCertificateFiles{
		ca: caPath, serverCertificate: serverCertificate, serverKey: serverKey,
		clientCertificate: clientCertificate, clientKey: clientKey,
	}
}

func writeSignedCertificate(
	t *testing.T,
	dir, name string,
	ca *x509.Certificate,
	caPrivate ed25519.PrivateKey,
	usage []x509.ExtKeyUsage,
	dnsNames []string,
) (string, string) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(int64(len(name) + 1)),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: usage, DNSNames: dnsNames,
	}
	certificate, err := x509.CreateCertificate(rand.Reader, template, ca, publicKey, caPrivate)
	require.NoError(t, err)
	certificatePath := filepath.Join(dir, name+".pem")
	writePEM(t, certificatePath, "CERTIFICATE", certificate)
	privateBody, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)
	keyPath := filepath.Join(dir, name+"-key.pem")
	writePEM(t, keyPath, "PRIVATE KEY", privateBody)

	return certificatePath, keyPath
}

func writePEM(t *testing.T, path, blockType string, body []byte) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{
		Type: blockType, Bytes: body,
	}), 0o600))
}
