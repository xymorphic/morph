package auth

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
)

const (
	privateKeyPEMType = "PRIVATE KEY"
	jwkKeyType        = "OKP"
	jwkCurve          = "Ed25519"
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
	publicKey, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		return Identity{}, errors.New("derive Ed25519 public key")
	}
	id, err := PublicKeyThumbprint(publicKey)
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

func ParseIdentity(privateKeyPEM []byte, generation uint64) (Identity, error) {
	block, rest := pem.Decode(privateKeyPEM)
	if block == nil || len(rest) != 0 || block.Type != privateKeyPEMType {
		return Identity{}, errors.New("parse Ed25519 private key PEM")
	}
	value, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return Identity{}, fmt.Errorf("parse Ed25519 PKCS#8 private key: %w", err)
	}
	privateKey, ok := value.(ed25519.PrivateKey)
	if !ok {
		return Identity{}, errors.New("private key must use Ed25519")
	}

	return IdentityFromPrivateKey(privateKey, generation)
}

func MarshalIdentity(identity Identity) ([]byte, error) {
	if len(identity.PrivateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("Ed25519 private key is required")
	}
	body, err := x509.MarshalPKCS8PrivateKey(identity.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("marshal Ed25519 private key: %w", err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: privateKeyPEMType, Bytes: body}), nil
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

func PublicKeyThumbprint(publicKey ed25519.PublicKey) (string, error) {
	jwk, err := PublicKeyJWK(publicKey)
	if err != nil {
		return "", err
	}
	canonical, err := json.Marshal(jwk)
	if err != nil {
		return "", fmt.Errorf("marshal public JWK: %w", err)
	}
	digest := sha256.Sum256(canonical)

	return base64.RawURLEncoding.EncodeToString(digest[:]), nil
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
