package browser

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/permissions"
)

const existingSessionWarning = "Personal browser attachment exposes signed-in sessions, cookies, and page data."
const wholeBrowserWarning = "Whole-browser attachment can control every visible browser context and target."
const unmanagedEgressWarning = "Attached browsers do not use Morph's managed egress proxy; " +
	"unrelated targets, browser services, existing connections, and direct UDP remain outside Morph's network enforcement. " +
	"The full_access preset does not provide managed network containment."
const attachmentIdentityKeyDomain = "morph/browser-attachment-identity/v1"

type CredentialResolver func(string) (string, error)

type attachment struct {
	identity      string
	authorization string
	scope         string
	contextID     string
	targetIDs     map[string]struct{}
}

func WithAttachmentIdentityKey(key []byte) ServiceOption {
	return func(service *Service) {
		service.SetAttachmentIdentityKey(key)
	}
}

func (s *Service) SetAttachmentIdentityKey(key []byte) {
	if s == nil {
		return
	}
	normalized := normalizeAttachmentIdentityKey(key)
	s.attachmentKeyMu.Lock()
	s.attachmentIdentityKey = normalized
	s.attachmentKeyMu.Unlock()

	s.mu.RLock()
	sessions := make([]*managedSession, 0, len(s.sessions))
	for _, runtime := range s.sessions {
		sessions = append(sessions, runtime)
	}
	s.mu.RUnlock()
	for _, runtime := range sessions {
		runtime.actionMu.Lock()
		if runtime.attachment.identity != "" {
			digest := hmac.New(sha256.New, normalized)
			_, _ = digest.Write([]byte(runtime.attachment.identity))
			runtime.attachment.identity = hex.EncodeToString(digest.Sum(nil))
		}
		runtime.actionMu.Unlock()
	}
}

func normalizeAttachmentIdentityKey(key []byte) []byte {
	if len(key) < 32 {
		return append([]byte(nil), key...)
	}
	digest := hmac.New(sha256.New, key)
	_, _ = digest.Write([]byte(attachmentIdentityKeyDomain))
	return digest.Sum(nil)
}

func WithCredentialResolver(resolve CredentialResolver) ServiceOption {
	return func(service *Service) {
		service.resolveCredential = resolve
	}
}

func (s *Service) resolveAttachment(profile config.BrowserProfileConfig) (attachment, error) {
	if profile.Mode != config.BrowserProfileRemoteCDP && profile.Mode != config.BrowserProfileExistingSession {
		return attachment{}, nil
	}
	if !profile.AcknowledgeUnmanagedEgress {
		return attachment{}, errors.New("attached browser profile must acknowledge unmanaged egress")
	}
	s.attachmentKeyMu.RLock()
	identityKey := append([]byte(nil), s.attachmentIdentityKey...)
	s.attachmentKeyMu.RUnlock()
	if len(identityKey) < 32 {
		return attachment{}, errors.New("browser attachment identity key is unavailable")
	}
	credential, err := s.resolveCredential(profile.CredentialRef)
	if err != nil {
		return attachment{}, err
	}
	authorization, err := getAuthorizationHeader(credential)
	if err != nil {
		return attachment{}, err
	}
	target, err := permissions.NetworkTargetFromURL(
		profile.CDPEndpoint, "CONNECT", permissions.NetworkRequestCDP,
	)
	if err != nil {
		return attachment{}, errors.New("browser CDP endpoint is invalid")
	}
	origin := fmt.Sprintf("%s://%s:%d", target.Scheme, target.Host, target.Port)
	targetIDs := slices.Clone(profile.TargetIDs)
	slices.Sort(targetIDs)
	targetIDs = slices.Compact(targetIDs)
	material := strings.Join([]string{
		profile.Mode, origin, target.Path, profile.DataIdentity, profile.AttachmentScope,
		profile.BrowserContextID, strings.Join(targetIDs, "\x00"), profile.CredentialRef, credential,
	}, "\x00")
	digest := hmac.New(sha256.New, identityKey)
	_, _ = digest.Write([]byte(material))
	targetSet := make(map[string]struct{}, len(targetIDs))
	for _, id := range targetIDs {
		targetSet[id] = struct{}{}
	}

	return attachment{
		identity: hex.EncodeToString(digest.Sum(nil)), authorization: authorization,
		scope: profile.AttachmentScope, contextID: profile.BrowserContextID, targetIDs: targetSet,
	}, nil
}

func resolveEnvironmentCredential(reference string) (string, error) {
	if reference == "" {
		return "", nil
	}
	name, ok := strings.CutPrefix(reference, "env:")
	if !ok || name == "" {
		return "", errors.New("browser CDP credential reference is invalid")
	}
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return "", errors.New("browser CDP credential is unavailable")
	}
	return strings.TrimSpace(value), nil
}

func getAuthorizationHeader(credential string) (string, error) {
	if credential == "" {
		return "", nil
	}
	if strings.ContainsAny(credential, "\r\n") {
		return "", errors.New("browser CDP credential is invalid")
	}
	if !strings.Contains(credential, " ") {
		return "Bearer " + credential, nil
	}
	scheme, value, ok := strings.Cut(credential, " ")
	if !ok || value == "" || !strings.EqualFold(scheme, "basic") && !strings.EqualFold(scheme, "bearer") {
		return "", errors.New("browser CDP credential must be a Basic or Bearer value")
	}
	return scheme + " " + value, nil
}

func GetProfileWarning(profile config.BrowserProfileConfig) string {
	warnings := make([]string, 0, 3)
	if profile.Mode == config.BrowserProfileRemoteCDP || profile.Mode == config.BrowserProfileExistingSession {
		warnings = append(warnings, unmanagedEgressWarning)
	}
	if profile.Mode == config.BrowserProfileExistingSession {
		warnings = append(warnings, existingSessionWarning)
	}
	if profile.AttachmentScope == config.BrowserAttachmentBrowser {
		warnings = append(warnings, wholeBrowserWarning)
	}
	return strings.Join(warnings, " ")
}
