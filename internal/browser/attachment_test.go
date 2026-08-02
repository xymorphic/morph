package browser

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xymorphic/morph/internal/config"
)

func TestService_ResolveAttachmentBindsCredentialIdentityAndScope(t *testing.T) {
	credential := "first-secret"
	service, err := NewService(
		context.Background(), testBrowserConfig(t), allowChecker(), &fakeBackend{},
		WithAttachmentIdentityKey(testAttachmentIdentityKey),
		WithCredentialResolver(func(reference string) (string, error) {
			require.Equal(t, "env:CDP_TOKEN", reference)
			return credential, nil
		}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	profile := config.BrowserProfileConfig{
		Name: "personal", Mode: config.BrowserProfileExistingSession,
		CDPEndpoint: "https://Browser.Example:443", CredentialRef: "env:CDP_TOKEN",
		DataIdentity: "daily-profile", AttachmentScope: config.BrowserAttachmentTargets,
		TargetIDs: []string{"target-b", "target-a"}, AcknowledgeUnmanagedEgress: true,
	}

	first, err := service.resolveAttachment(profile)
	require.NoError(t, err)
	require.Equal(t, "Bearer first-secret", first.authorization)
	require.NotContains(t, first.identity, credential)
	require.Contains(t, first.targetIDs, "target-a")

	profile.TargetIDs = []string{"target-a", "target-b"}
	reordered, err := service.resolveAttachment(profile)
	require.NoError(t, err)
	require.Equal(t, first.identity, reordered.identity)

	service.SetAttachmentIdentityKey([]byte("abcdef0123456789abcdef0123456789"))
	rebound, err := service.resolveAttachment(profile)
	require.NoError(t, err)
	require.NotEqual(t, first.identity, rebound.identity)

	credential = "rotated-secret"
	rotated, err := service.resolveAttachment(profile)
	require.NoError(t, err)
	require.NotEqual(t, rebound.identity, rotated.identity)

	profile.AttachmentScope = config.BrowserAttachmentBrowser
	profile.TargetIDs = nil
	broader, err := service.resolveAttachment(profile)
	require.NoError(t, err)
	require.NotEqual(t, rotated.identity, broader.identity)

	profile.CDPEndpoint = "https://browser.example:443/devtools/browser/another"
	differentEndpoint, err := service.resolveAttachment(profile)
	require.NoError(t, err)
	require.NotEqual(t, broader.identity, differentEndpoint.identity)
}

func TestAttachmentCredentialsValidateSafeAuthorizationValues(t *testing.T) {
	t.Setenv("MORPH_TEST_CDP", " token ")
	value, err := resolveEnvironmentCredential("env:MORPH_TEST_CDP")
	require.NoError(t, err)
	require.Equal(t, "token", value)
	require.Equal(t, "Bearer token", mustGetAuthorizationHeader(t, value))
	require.Equal(t, "Basic abc", mustGetAuthorizationHeader(t, "Basic abc"))

	_, err = resolveEnvironmentCredential("env:MISSING_MORPH_TEST_CDP")
	require.EqualError(t, err, "browser CDP credential is unavailable")
	_, err = resolveEnvironmentCredential("secret")
	require.EqualError(t, err, "browser CDP credential reference is invalid")
	_, err = getAuthorizationHeader("Digest value")
	require.EqualError(t, err, "browser CDP credential must be a Basic or Bearer value")
	_, err = getAuthorizationHeader("token\r\ninjected")
	require.EqualError(t, err, "browser CDP credential is invalid")
}

func TestService_ResolveAttachmentRequiresUnmanagedEgressAcknowledgement(t *testing.T) {
	service, err := NewService(
		context.Background(), testBrowserConfig(t), allowChecker(), &fakeBackend{},
		WithAttachmentIdentityKey(testAttachmentIdentityKey),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	_, err = service.resolveAttachment(config.BrowserProfileConfig{
		Mode: config.BrowserProfileRemoteCDP, CDPEndpoint: "https://example.com",
		AttachmentScope: config.BrowserAttachmentBrowser,
	})
	require.EqualError(t, err, "attached browser profile must acknowledge unmanaged egress")
	require.Contains(t, GetProfileWarning(config.BrowserProfileConfig{
		Mode: config.BrowserProfileRemoteCDP, AttachmentScope: config.BrowserAttachmentBrowser,
	}), "do not use Morph's managed egress proxy")
}

func TestService_ResolveAttachmentFailsClosedWithoutIdentityOrCredential(t *testing.T) {
	profile := config.BrowserProfileConfig{
		Mode: config.BrowserProfileRemoteCDP, CDPEndpoint: "https://example.com",
		AttachmentScope: config.BrowserAttachmentBrowser, AcknowledgeUnmanagedEgress: true,
	}
	service, err := NewService(context.Background(), testBrowserConfig(t), allowChecker(), &fakeBackend{})
	require.NoError(t, err)
	_, err = service.resolveAttachment(profile)
	require.EqualError(t, err, "browser attachment identity key is unavailable")
	require.NoError(t, service.Close(context.Background()))

	service, err = NewService(
		context.Background(), testBrowserConfig(t), allowChecker(), &fakeBackend{},
		WithAttachmentIdentityKey([]byte("too-short")),
	)
	require.NoError(t, err)
	_, err = service.resolveAttachment(profile)
	require.EqualError(t, err, "browser attachment identity key is unavailable")
	require.NoError(t, service.Close(context.Background()))

	service, err = NewService(
		context.Background(), testBrowserConfig(t), allowChecker(), &fakeBackend{},
		WithAttachmentIdentityKey(testAttachmentIdentityKey),
		WithCredentialResolver(func(string) (string, error) { return "", errors.New("credential lookup failed") }),
	)
	require.NoError(t, err)
	profile.CredentialRef = "env:CDP_TOKEN"
	_, err = service.resolveAttachment(profile)
	require.EqualError(t, err, "credential lookup failed")
	require.NoError(t, service.Close(context.Background()))

	service, err = NewService(
		context.Background(), testBrowserConfig(t), allowChecker(), &fakeBackend{},
		WithAttachmentIdentityKey(testAttachmentIdentityKey),
		WithCredentialResolver(func(string) (string, error) { return "Digest secret", nil }),
	)
	require.NoError(t, err)
	_, err = service.resolveAttachment(profile)
	require.EqualError(t, err, "browser CDP credential must be a Basic or Bearer value")
	profile.CredentialRef = ""
	profile.CDPEndpoint = "not-an-endpoint"
	service.resolveCredential = func(string) (string, error) { return "", nil }
	_, err = service.resolveAttachment(profile)
	require.EqualError(t, err, "browser CDP endpoint is invalid")
	require.NoError(t, service.Close(context.Background()))
}

func mustGetAuthorizationHeader(t *testing.T, value string) string {
	t.Helper()
	header, err := getAuthorizationHeader(value)
	require.NoError(t, err)
	return header
}
