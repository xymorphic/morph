package pairing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"github.com/xymorphic/morph/pkg/str"
)

const (
	DefaultPeriodSeconds = 30
	DefaultSkew          = 1
	DefaultDigits        = otp.DigitsEight
	DefaultRequestTTL    = time.Hour
	DefaultPendingLimit  = 100
)

var (
	ErrSecretRequired = errors.New("gateway pairing secret is required")
	ErrAmbiguousCode  = errors.New("gateway pairing code matches multiple pending requests")
	ErrPendingLimit   = errors.New("gateway pairing pending request limit reached")
)

type Identity struct {
	Source      string
	SenderID    string
	DisplayName string
	Metadata    map[string]string
}

type PendingRequest struct {
	CreatedAt   time.Time
	LastSeenAt  time.Time
	ExpiresAt   time.Time
	Source      string
	SenderID    string
	DisplayName string
	Metadata    map[string]string
}

type ApprovedSender struct {
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Source      string
	SenderID    string
	DisplayName string
	Metadata    map[string]string
}

type Challenge struct {
	Request PendingRequest
	Code    string
}

type Store interface {
	SaveGatewayPairingRequest(context.Context, PendingRequest) error
	GetGatewayPairingRequest(context.Context, string, string) (PendingRequest, bool, error)
	ListGatewayPairingRequests(context.Context, string) ([]PendingRequest, error)
	DeleteGatewayPairingRequest(context.Context, string, string) error
	ClearGatewayPairingRequests(context.Context, string) error
	SaveGatewayPairedSender(context.Context, ApprovedSender) error
	GetGatewayPairedSender(context.Context, string, string) (ApprovedSender, bool, error)
	ListGatewayPairedSenders(context.Context, string) ([]ApprovedSender, error)
	DeleteGatewayPairedSender(context.Context, string, string) error
}

type Manager struct {
	store        Store
	secret       string
	period       uint
	skew         uint
	digits       otp.Digits
	requestTTL   time.Duration
	pendingLimit int
	now          func() time.Time
	verifyCode   func(string, string, string, time.Time) (bool, error)
}

type Options struct {
	Store        Store
	Secret       string
	Period       time.Duration
	Skew         uint
	Digits       otp.Digits
	RequestTTL   time.Duration
	PendingLimit int
	Now          func() time.Time
}

func NewManager(opts Options) *Manager {
	secretValue := str.String(opts.Secret)
	manager := &Manager{
		store:        opts.Store,
		secret:       secretValue.Trim(),
		period:       DefaultPeriodSeconds,
		skew:         DefaultSkew,
		digits:       DefaultDigits,
		requestTTL:   DefaultRequestTTL,
		pendingLimit: DefaultPendingLimit,
		now:          func() time.Time { return time.Now().UTC() },
	}
	if opts.Period > 0 {
		manager.period = uint(opts.Period.Seconds())
	}
	if opts.Skew != 0 {
		manager.skew = opts.Skew
	}
	if opts.Digits != 0 {
		manager.digits = opts.Digits
	}
	if opts.RequestTTL > 0 {
		manager.requestTTL = opts.RequestTTL
	}
	if opts.PendingLimit > 0 {
		manager.pendingLimit = opts.PendingLimit
	}
	if opts.Now != nil {
		manager.now = opts.Now
	}

	return manager
}

func (m *Manager) Request(ctx context.Context, identity Identity) (Challenge, error) {
	if err := m.checkReady(); err != nil {
		return Challenge{}, err
	}

	identity = normalizeIdentity(identity)
	if identity.Source == "" {
		return Challenge{}, errors.New("gateway pairing source is required")
	}
	if identity.SenderID == "" {
		return Challenge{}, errors.New("gateway pairing sender id is required")
	}

	now := m.now().UTC()
	existing, ok, err := m.store.GetGatewayPairingRequest(ctx, identity.Source, identity.SenderID)
	if err != nil {
		return Challenge{}, err
	}
	if ok && existing.ExpiresAt.After(now) {
		existing.LastSeenAt = now
		existing.DisplayName = identity.DisplayName
		existing.Metadata = cloneMap(identity.Metadata)
		if err := m.store.SaveGatewayPairingRequest(ctx, existing); err != nil {
			return Challenge{}, err
		}
		code, err := m.Code(identity.Source, identity.SenderID, now)
		return Challenge{Request: existing, Code: code}, err
	}

	requests, err := m.store.ListGatewayPairingRequests(ctx, identity.Source)
	if err != nil {
		return Challenge{}, err
	}
	active := 0
	for _, request := range requests {
		if request.ExpiresAt.After(now) {
			active++
		}
	}
	if active >= m.pendingLimit {
		return Challenge{}, ErrPendingLimit
	}

	request := PendingRequest{
		Source:      identity.Source,
		SenderID:    identity.SenderID,
		DisplayName: identity.DisplayName,
		Metadata:    cloneMap(identity.Metadata),
		CreatedAt:   now,
		LastSeenAt:  now,
		ExpiresAt:   now.Add(m.requestTTL),
	}
	if err := m.store.SaveGatewayPairingRequest(ctx, request); err != nil {
		return Challenge{}, err
	}
	code, err := m.Code(identity.Source, identity.SenderID, now)
	return Challenge{Request: request, Code: code}, err
}

func (m *Manager) Code(source string, senderID string, at time.Time) (string, error) {
	if err := m.checkReady(); err != nil {
		return "", err
	}

	senderIDValue := str.String(senderID)
	secret := deriveTOTPSecret(m.secret, normalizeSource(source), senderIDValue.Trim())
	return totp.GenerateCodeCustom(secret, at.UTC(), m.validateOpts())
}

func (m *Manager) Verify(source string, senderID string, code string, at time.Time) (bool, error) {
	if err := m.checkReady(); err != nil {
		return false, err
	}
	if m.verifyCode != nil {
		return m.verifyCode(source, senderID, code, at)
	}

	senderIDValue2 := str.String(senderID)
	secret := deriveTOTPSecret(m.secret, normalizeSource(source), senderIDValue2.Trim())
	codeValue := str.String(code)
	return totp.ValidateCustom(codeValue.Trim(), secret, at.UTC(), m.validateOpts())
}

func (m *Manager) Approve(ctx context.Context, source string, code string) (ApprovedSender, bool, error) {
	if err := m.checkReady(); err != nil {
		return ApprovedSender{}, false, err
	}

	source = normalizeSource(source)
	codeValue2 := str.String(code)
	code = codeValue2.Trim()
	if source == "" {
		return ApprovedSender{}, false, errors.New("gateway pairing source is required")
	}
	if code == "" {
		return ApprovedSender{}, false, nil
	}

	now := m.now().UTC()
	requests, err := m.store.ListGatewayPairingRequests(ctx, source)
	if err != nil {
		return ApprovedSender{}, false, err
	}

	var matches []PendingRequest
	for _, request := range requests {
		if !request.ExpiresAt.After(now) {
			continue
		}

		ok, err := m.Verify(source, request.SenderID, code, now)
		if err != nil {
			return ApprovedSender{}, false, err
		}
		if ok {
			matches = append(matches, request)
		}
	}
	if len(matches) == 0 {
		return ApprovedSender{}, false, nil
	}
	if len(matches) > 1 {
		return ApprovedSender{}, false, ErrAmbiguousCode
	}

	request := matches[0]
	approved := ApprovedSender{
		Source:      request.Source,
		SenderID:    request.SenderID,
		DisplayName: request.DisplayName,
		Metadata:    cloneMap(request.Metadata),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := m.store.SaveGatewayPairedSender(ctx, approved); err != nil {
		return ApprovedSender{}, false, err
	}
	if err := m.store.DeleteGatewayPairingRequest(ctx, source, request.SenderID); err != nil {
		return ApprovedSender{}, false, err
	}

	return approved, true, nil
}

func (m *Manager) Revoke(ctx context.Context, source string, senderID string) error {
	if m == nil || m.store == nil {
		return errors.New("gateway pairing store is required")
	}

	senderIDValue3 := str.String(senderID)
	return m.store.DeleteGatewayPairedSender(ctx, normalizeSource(source), senderIDValue3.Trim())
}

func (m *Manager) IsApproved(ctx context.Context, source string, senderID string) (bool, error) {
	if m == nil || m.store == nil {
		return false, errors.New("gateway pairing store is required")
	}

	senderIDValue4 := str.String(senderID)
	_, ok, err := m.store.GetGatewayPairedSender(ctx, normalizeSource(source), senderIDValue4.Trim())
	return ok, err
}

func (m *Manager) validateOpts() totp.ValidateOpts {
	return totp.ValidateOpts{
		Period:    m.period,
		Skew:      m.skew,
		Digits:    m.digits,
		Algorithm: otp.AlgorithmSHA1,
	}
}

func (m *Manager) checkReady() error {
	if m == nil || m.store == nil {
		return errors.New("gateway pairing store is required")
	}

	secretValue2 := str.String(m.secret)
	if secretValue2.Trim() == "" {
		return ErrSecretRequired
	}

	return nil
}

func normalizeIdentity(identity Identity) Identity {
	senderIDValue5 := str.String(identity.SenderID)
	displayNameValue := str.String(identity.DisplayName)
	return Identity{
		Source:      normalizeSource(identity.Source),
		SenderID:    senderIDValue5.Trim(),
		DisplayName: displayNameValue.Trim(),
		Metadata:    cloneMap(identity.Metadata),
	}
}

func normalizeSource(source string) string {
	sourceValue := str.String(source)
	return sourceValue.Normalized()
}

func deriveTOTPSecret(secret string, source string, senderID string) string {
	secretValue3 := str.String(secret)
	mac := hmac.New(sha256.New, []byte(secretValue3.Trim()))
	mac.Write([]byte(source))
	mac.Write([]byte{0})
	senderIDValue6 := str.String(senderID)
	mac.Write([]byte(senderIDValue6.Trim()))
	sum := mac.Sum(nil)

	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum)
}

func cloneMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}

	clone := make(map[string]string, len(values))
	for key, value := range values {
		keyValue := str.String(key)
		key = keyValue.Trim()
		if key == "" {
			continue
		}
		valueText := str.String(value)
		clone[key] = valueText.Trim()
	}
	if len(clone) == 0 {
		return nil
	}

	return clone
}
