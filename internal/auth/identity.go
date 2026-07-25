package auth

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const (
	privateKeySeedHexLength = ed25519.SeedSize * 2
	privateKeyHexLength     = ed25519.PrivateKeySize * 2
	identityIDBytes         = 20
	jwkKeyType              = "OKP"
	jwkCurve                = "Ed25519"
)

type Identity struct {
	ID         string
	Generation uint64
	PrivateKey ed25519.PrivateKey
	PublicKey  ed25519.PublicKey
}

type PublicJWK struct {
	Curve   string `json:"crv"`
	KeyType string `json:"kty"`
	X       string `json:"x"`
}

func GenerateIdentity(generation uint64) (Identity, error) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Identity{}, fmt.Errorf("generate identity: %w", err)
	}

	return IdentityFromPrivateKey(privateKey, generation)
}

func IdentityFromPrivateKey(privateKey ed25519.PrivateKey, generation uint64) (Identity, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return Identity{}, errors.New("Ed25519 private key is required")
	}
	if subtle.ConstantTimeCompare(privateKey, ed25519.NewKeyFromSeed(privateKey.Seed())) != 1 {
		return Identity{}, errors.New("Ed25519 private key is inconsistent")
	}
	publicKey, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		return Identity{}, errors.New("derive Ed25519 public key")
	}
	id, err := PublicKeyIdentityID(publicKey)
	if err != nil {
		return Identity{}, err
	}
	if generation == 0 {
		generation = 1
	}

	return Identity{
		ID:         id,
		Generation: generation,
		PrivateKey: append(ed25519.PrivateKey(nil), privateKey...),
		PublicKey:  append(ed25519.PublicKey(nil), publicKey...),
	}, nil
}

func ParseIdentity(encodedPrivateKey []byte, generation uint64) (Identity, error) {
	encoded := strings.TrimSpace(string(encodedPrivateKey))
	decoded, err := hex.DecodeString(encoded)
	if err != nil {
		return Identity{}, errors.New("Ed25519 private key must be hexadecimal")
	}

	switch len(encoded) {
	case privateKeySeedHexLength:
		return IdentityFromPrivateKey(ed25519.NewKeyFromSeed(decoded), generation)
	case privateKeyHexLength:
		return IdentityFromPrivateKey(decoded, generation)
	default:
		return Identity{}, errors.New(
			"Ed25519 private key must be 64 or 128 hexadecimal characters",
		)
	}
}

func MarshalIdentity(identity Identity) ([]byte, error) {
	if len(identity.PrivateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("Ed25519 private key is required")
	}

	return []byte(hex.EncodeToString(identity.PrivateKey.Seed())), nil
}

func IsEncodedIdentity(encodedPrivateKey []byte) bool {
	encoded := strings.TrimSpace(string(encodedPrivateKey))
	if len(encoded) != privateKeySeedHexLength && len(encoded) != privateKeyHexLength {
		return false
	}
	_, err := hex.DecodeString(encoded)

	return err == nil
}

func PublicKeyJWK(publicKey ed25519.PublicKey) (PublicJWK, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return PublicJWK{}, errors.New("Ed25519 public key is required")
	}

	return PublicJWK{
		Curve:   jwkCurve,
		KeyType: jwkKeyType,
		X:       base64.RawURLEncoding.EncodeToString(publicKey),
	}, nil
}

func PublicKeyIdentityID(publicKey ed25519.PublicKey) (string, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return "", errors.New("Ed25519 public key is required")
	}
	digest := sha256.Sum256(publicKey)

	return hex.EncodeToString(digest[len(digest)-identityIDBytes:]), nil
}

func DeriveSecret(identity Identity, purpose string) ([]byte, error) {
	if len(identity.PrivateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("Ed25519 private key is required")
	}
	if purpose == "" {
		return nil, errors.New("derivation purpose is required")
	}
	mac := hmac.New(sha256.New, identity.PrivateKey.Seed())
	_, _ = mac.Write([]byte("morph/v1/"))
	_, _ = mac.Write([]byte(purpose))

	return mac.Sum(nil), nil
}
