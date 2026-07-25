package auth_test

import (
	"crypto/ed25519"
	"testing"

	"github.com/stretchr/testify/require"

	morphauth "github.com/wandxy/morph/internal/auth"
)

func TestIdentity_RoundTripsPKCS8AndUsesStableThumbprint(t *testing.T) {
	identity, err := morphauth.GenerateIdentity(3)
	require.NoError(t, err)
	require.Len(t, identity.PrivateKey, ed25519.PrivateKeySize)
	require.Len(t, identity.PublicKey, ed25519.PublicKeySize)

	encoded, err := morphauth.MarshalIdentity(identity)
	require.NoError(t, err)
	parsed, err := morphauth.ParseIdentity(encoded, 3)
	require.NoError(t, err)

	require.Equal(t, identity.ID, parsed.ID)
	require.Equal(t, identity.Generation, parsed.Generation)
	require.Equal(t, identity.PrivateKey, parsed.PrivateKey)
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
	_, err = morphauth.ParseIdentity([]byte("not pem"), 1)
	require.EqualError(t, err, "parse Ed25519 private key PEM")
	_, err = morphauth.MarshalIdentity(morphauth.Identity{})
	require.EqualError(t, err, "Ed25519 private key is required")
}

func TestSecret_NeverFormatsItsValue(t *testing.T) {
	secret := morphauth.NewSecret("sensitive")

	require.Equal(t, "sensitive", secret.Reveal())
	require.Equal(t, "[REDACTED]", secret.String())
	require.Equal(t, "[REDACTED]", secret.GoString())
}
