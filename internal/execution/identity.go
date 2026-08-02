package execution

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

var (
	ErrInvalidProcessID = errors.New("invalid_process_id")
	ErrProcessStale     = errors.New("process_stale")
	ErrProcessNotFound  = errors.New("process_not_found")
	ErrProcessDenied    = errors.New("process_access_denied")
	randomRead          = rand.Read
)

type Owner struct {
	Profile            string `json:"profile"`
	ActorKind          string `json:"actor_kind"`
	ActorID            string `json:"actor_id,omitempty"`
	Surface            string `json:"surface"`
	PublicSessionID    string `json:"public_session_id"`
	EffectiveSessionID string `json:"effective_session_id"`
	RunID              string `json:"run_id,omitempty"`
}

func (o Owner) Normalize() (Owner, error) {
	o.Profile = strings.TrimSpace(o.Profile)
	o.ActorKind = strings.TrimSpace(strings.ToLower(o.ActorKind))
	o.ActorID = strings.TrimSpace(o.ActorID)
	o.Surface = strings.TrimSpace(strings.ToLower(o.Surface))
	o.PublicSessionID = strings.TrimSpace(o.PublicSessionID)
	o.EffectiveSessionID = strings.TrimSpace(o.EffectiveSessionID)
	o.RunID = strings.TrimSpace(o.RunID)
	if o.Profile == "" || o.ActorKind == "" || o.Surface == "" || o.PublicSessionID == "" ||
		o.EffectiveSessionID == "" {
		return Owner{}, errors.New("execution owner identity is incomplete")
	}
	return o, nil
}

func (o Owner) Fingerprint() string {
	normalized, err := o.Normalize()
	if err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		normalized.Profile,
		normalized.ActorKind,
		normalized.ActorID,
		normalized.Surface,
		normalized.PublicSessionID,
		normalized.EffectiveSessionID,
	}, "\x00")))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

type ProcessIdentity struct {
	Version              int    `json:"version"`
	OwnerFingerprint     string `json:"owner"`
	SecurityGeneration   string `json:"security_generation"`
	DaemonIncarnation    string `json:"daemon_incarnation"`
	ContainerIncarnation string `json:"container_incarnation"`
	Token                string `json:"token"`
}

type ProcessCodec struct {
	key                []byte
	securityGeneration string
	daemonIncarnation  string
}

func NewProcessCodec(
	key []byte,
	securityGeneration string,
	daemonIncarnation string,
) (*ProcessCodec, error) {
	if len(key) < 32 || strings.TrimSpace(securityGeneration) == "" ||
		strings.TrimSpace(daemonIncarnation) == "" {
		return nil, errors.New("process identity codec configuration is incomplete")
	}
	return &ProcessCodec{
		key:                append([]byte(nil), key...),
		securityGeneration: strings.TrimSpace(securityGeneration),
		daemonIncarnation:  strings.TrimSpace(daemonIncarnation),
	}, nil
}

func NewIncarnation() (string, error) {
	raw := make([]byte, 24)
	if _, err := randomRead(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (c *ProcessCodec) Encode(
	owner Owner,
	containerIncarnation string,
	token string,
) (string, error) {
	if c == nil {
		return "", ErrInvalidProcessID
	}
	owner, err := owner.Normalize()
	if err != nil || strings.TrimSpace(containerIncarnation) == "" ||
		strings.TrimSpace(token) == "" {
		return "", ErrInvalidProcessID
	}
	payload, _ := json.Marshal(ProcessIdentity{
		Version:              1,
		OwnerFingerprint:     owner.Fingerprint(),
		SecurityGeneration:   c.securityGeneration,
		DaemonIncarnation:    c.daemonIncarnation,
		ContainerIncarnation: strings.TrimSpace(containerIncarnation),
		Token:                strings.TrimSpace(token),
	})
	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(
		payload,
	) + "." + base64.RawURLEncoding.EncodeToString(
		mac.Sum(nil),
	), nil
}

func (c *ProcessCodec) Decode(
	value string,
	owner Owner,
	containerIncarnation string,
) (ProcessIdentity, error) {
	identity, err := c.DecodeCurrent(value, owner)
	if err != nil {
		return identity, err
	}
	if identity.ContainerIncarnation != strings.TrimSpace(containerIncarnation) {
		return identity, ErrProcessStale
	}
	return identity, nil
}

func (c *ProcessCodec) DecodeCurrent(value string, owner Owner) (ProcessIdentity, error) {
	identity, err := c.Verify(value)
	if err != nil {
		return ProcessIdentity{}, err
	}
	if identity.SecurityGeneration != c.securityGeneration ||
		identity.DaemonIncarnation != c.daemonIncarnation {
		return identity, ErrProcessStale
	}
	if identity.OwnerFingerprint != owner.Fingerprint() {
		return identity, ErrProcessDenied
	}
	return identity, nil
}

func (c *ProcessCodec) Verify(value string) (ProcessIdentity, error) {
	if c == nil {
		return ProcessIdentity{}, ErrInvalidProcessID
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return ProcessIdentity{}, ErrInvalidProcessID
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ProcessIdentity{}, ErrInvalidProcessID
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ProcessIdentity{}, ErrInvalidProcessID
	}
	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return ProcessIdentity{}, ErrInvalidProcessID
	}
	var identity ProcessIdentity
	if json.Unmarshal(payload, &identity) != nil || identity.Version != 1 || identity.Token == "" {
		return ProcessIdentity{}, ErrInvalidProcessID
	}
	return identity, nil
}
