package auth_test

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	morphauth "github.com/wandxy/morph/internal/auth"
)

func TestIdentity_RoundTripsSeedHexAndUsesStableThumbprint(t *testing.T) {
	identity, err := morphauth.GenerateIdentity(3)
	require.NoError(t, err)
	require.Len(t, identity.PrivateKey, ed25519.PrivateKeySize)
	require.Len(t, identity.PublicKey, ed25519.PublicKeySize)

	encoded, err := morphauth.MarshalIdentity(identity)
	require.NoError(t, err)
	require.Len(t, encoded, ed25519.SeedSize*2)
	require.Equal(t, hex.EncodeToString(identity.PrivateKey.Seed()), string(encoded))
	require.True(t, morphauth.IsEncodedIdentity(encoded))
	parsed, err := morphauth.ParseIdentity(encoded, 3)
	require.NoError(t, err)

	require.Equal(t, identity.ID, parsed.ID)
	require.Len(t, identity.ID, 40)
	_, err = hex.DecodeString(identity.ID)
	require.NoError(t, err)
	digest := sha256.Sum256(identity.PublicKey)
	require.Equal(t, hex.EncodeToString(digest[sha256.Size-20:]), identity.ID)
	require.Equal(t, identity.Generation, parsed.Generation)
	require.Equal(t, identity.PrivateKey, parsed.PrivateKey)
}

func TestIdentity_AcceptsFullPrivateKeyHex(t *testing.T) {
	identity, err := morphauth.GenerateIdentity(3)
	require.NoError(t, err)

	encoded := []byte(hex.EncodeToString(identity.PrivateKey))
	require.True(t, morphauth.IsEncodedIdentity(encoded))
	parsed, err := morphauth.ParseIdentity(encoded, identity.Generation)
	require.NoError(t, err)
	require.Equal(t, identity, parsed)
}

func TestIdentity_DerivesDomainSeparatedSecrets(t *testing.T) {
	identity, err := morphauth.GenerateIdentity(1)
	require.NoError(t, err)

	browserKey, err := morphauth.DeriveSecret(identity, "browser-attachment")
	require.NoError(t, err)
	commandKey, err := morphauth.DeriveSecret(identity, "command-approval")
	require.NoError(t, err)

	require.Len(t, browserKey, 32)
	require.NotEqual(t, browserKey, commandKey)
}

func TestIdentity_RejectsInvalidPrivateKeys(t *testing.T) {
	_, err := morphauth.IdentityFromPrivateKey([]byte("short"), 1)
	require.EqualError(t, err, "Ed25519 private key is required")
	inconsistent := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	inconsistent[len(inconsistent)-1] = 1
	_, err = morphauth.IdentityFromPrivateKey(inconsistent, 1)
	require.EqualError(t, err, "Ed25519 private key is inconsistent")
	_, err = morphauth.ParseIdentity([]byte("not hex"), 1)
	require.EqualError(t, err, "Ed25519 private key must be hexadecimal")
	_, err = morphauth.ParseIdentity(
		[]byte("zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"+
			"zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"),
		1,
	)
	require.EqualError(t, err, "Ed25519 private key must be hexadecimal")
	_, err = morphauth.ParseIdentity(
		[]byte("-----BEGIN PRIVATE KEY-----\nlegacy\n-----END PRIVATE KEY-----"),
		1,
	)
	require.EqualError(t, err, "Ed25519 private key must be hexadecimal")
	_, err = morphauth.ParseIdentity([]byte("abcd"), 1)
	require.EqualError(t, err, "Ed25519 private key must be 64 or 128 hexadecimal characters")
	_, err = morphauth.MarshalIdentity(morphauth.Identity{})
	require.EqualError(t, err, "Ed25519 private key is required")
}

func TestSecret_NeverFormatsItsValue(t *testing.T) {
	secret := morphauth.NewSecret("sensitive")

	require.Equal(t, "sensitive", secret.Reveal())
	require.Equal(t, "[REDACTED]", secret.String())
	require.Equal(t, "[REDACTED]", secret.GoString())
}
