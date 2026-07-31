package guardrails

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeRedactsSensitiveMapFields(t *testing.T) {
	value := Sanitize(map[string]any{
		"authorization":      "Bearer top-secret-token",
		"ChatGPT-Account-ID": "acct-secret",
		"nested": map[string]any{
			"api_key": "sk-123456",
		},
		"safe": "hello",
	})

	sanitized := value.(map[string]any)
	require.Equal(t, "[REDACTED]", sanitized["authorization"])
	require.Equal(t, "[REDACTED]", sanitized["ChatGPT-Account-ID"])
	require.Equal(t, "hello", sanitized["safe"])
	require.Equal(t, "[REDACTED]", sanitized["nested"].(map[string]any)["api_key"])
}

func TestSanitizeRedactsSensitiveJSONString(t *testing.T) {
	value := Sanitize(`{"key":"sk-secret","refresh":"refresh-secret","safe":"ok","token":"abc"}`)
	require.Equal(t, `{"key":"[REDACTED]","refresh":"[REDACTED]","safe":"ok","token":"[REDACTED]"}`, value)
}

func TestSanitizeRedactsBearerAndKeyStrings(t *testing.T) {
	value := Sanitize("Authorization: Bearer abc.def and sk-proj-abc123def456ghi789jkl012")
	require.Equal(t, "Authorization: Bearer *** and sk-pro...l012", value)
}

func TestSanitizeRedactsCommonAuthorizationHeaderSchemes(t *testing.T) {
	value := Sanitize("Authorization: Basic dXNlcjpwYXNzd29yZA== Authorization: Token supersecrettokenvalue123456 Authorization: ApiKey sk-proj-abc123def456ghi789jkl012")
	require.Equal(t, "Authorization: Basic dXNlcj...ZA== Authorization: Token supers...3456 Authorization: ApiKey sk-pro...l012", value)
}

func TestSanitizeLeavesUnsupportedAuthorizationSchemeUntouched(t *testing.T) {
	value := Sanitize("Authorization: Digest username=\"alice\"")
	require.Equal(t, "Authorization: Digest username=\"alice\"", value)
}

func TestSanitizeLeavesSafeValuesUntouched(t *testing.T) {
	value := Sanitize(map[string]any{"message": "hello", "count": 2})
	require.Equal(t, map[string]any{"message": "hello", "count": 2}, value)
}

func TestNewRedactorSanitizesStringSlices(t *testing.T) {
	redactor := NewRedactor()
	value := redactor.Sanitize([]string{"Bearer token", "ok"})
	require.Equal(t, []string{"Bearer ***", "ok"}, value)
}

func TestSanitizeReturnsOriginalValueWhenMarshalFails(t *testing.T) {
	value := Sanitize(make(chan int))
	require.IsType(t, (chan int)(nil), value)
}

func TestSanitizeHandlesNil(t *testing.T) {
	require.Nil(t, Sanitize(nil))
}

func TestSanitizeHandlesAnySlice(t *testing.T) {
	value := Sanitize([]any{"Bearer token", map[string]any{"token": "x"}})
	require.Equal(t, []any{"Bearer ***", map[string]any{"token": "[REDACTED]"}}, value)
}

func TestSanitizeLeavesPrimitiveScalarsUntouched(t *testing.T) {
	require.Equal(t, true, Sanitize(true))
	require.Equal(t, 42, Sanitize(42))
	require.Equal(t, int64(42), Sanitize(int64(42)))
	require.Equal(t, int32(42), Sanitize(int32(42)))
	require.Equal(t, int16(42), Sanitize(int16(42)))
	require.Equal(t, int8(42), Sanitize(int8(42)))
	require.Equal(t, uint(7), Sanitize(uint(7)))
	require.Equal(t, uint64(7), Sanitize(uint64(7)))
	require.Equal(t, uint32(7), Sanitize(uint32(7)))
	require.Equal(t, uint16(7), Sanitize(uint16(7)))
	require.Equal(t, uint8(7), Sanitize(uint8(7)))
	require.Equal(t, float32(1.5), Sanitize(float32(1.5)))
	require.Equal(t, 1.5, Sanitize(1.5))
}

func TestSanitizeStringReturnsOriginalWhitespace(t *testing.T) {
	require.Equal(t, "   ", Sanitize("   "))
}

func TestSanitizeStringPreservesNonSensitiveJSONShape(t *testing.T) {
	value := Sanitize(`{"message":"hello","count":2}`)
	require.Equal(t, `{"count":2,"message":"hello"}`, value)
}

func TestSanitizeStringSliceDirectly(t *testing.T) {
	value := Sanitize([]string{"Bearer token", "sk-proj-abc123def456ghi789jkl012", "ok"})
	require.Equal(t, []string{"Bearer ***", "sk-pro...l012", "ok"}, value)
}

func TestSanitizeFallsBackToSanitizedRawStringWhenJSONUnmarshalFails(t *testing.T) {
	originalJSONUnmarshal := jsonUnmarshal
	jsonUnmarshal = func([]byte, any) error {
		return assert.AnError
	}
	defer func() {
		jsonUnmarshal = originalJSONUnmarshal
	}()

	value := Sanitize(struct {
		Token string `json:"token"`
	}{Token: "sk-proj-abc123def456ghi789jkl012"})
	require.Equal(t, `{"token": "sk-pro...l012"}`, value)
}

func TestSanitizeNormalizesStructsThroughJSONRoundTrip(t *testing.T) {
	value := Sanitize(struct {
		Token string `json:"token"`
		Safe  string `json:"safe"`
	}{
		Token: "sk-secret",
		Safe:  "ok",
	})

	require.Equal(t, map[string]any{
		"token": "[REDACTED]",
		"safe":  "ok",
	}, value)
}

func TestSanitizeRedactsKnownPrefixes(t *testing.T) {
	value := Sanitize("ghp_abc123def456ghi789jkl")
	require.Equal(t, "ghp_ab...9jkl", value)
}

func TestSanitizeRedactsEnvAssignments(t *testing.T) {
	value := Sanitize(`OPENAI_API_KEY=sk-proj-abc123def456ghi789jkl012 MY_SECRET_TOKEN="supersecretvalue123456789" HOME=/tmp`)
	require.Equal(t, `OPENAI_API_KEY=sk-pro...l012 MY_SECRET_TOKEN="supers...6789" HOME=/tmp`, value)
}

func TestSanitizeDoesNotRedactNonSecretAuthLikeEnvNames(t *testing.T) {
	value := Sanitize(`AUTHOR=alice AUTHORS=bob OAUTH_CALLBACK=https://example.com/callback`)
	require.Equal(t, `AUTHOR=alice AUTHORS=bob OAUTH_CALLBACK=https://example.com/callback`, value)
}

func TestSanitizeRedactsExplicitAuthTokenEnvNames(t *testing.T) {
	value := Sanitize(`AUTH_TOKEN=supersecrettokenvalue123456 REFRESH_TOKEN=refreshsecretvalue123456`)
	require.Equal(t, `AUTH_TOKEN=supers...3456 REFRESH_TOKEN=refres...3456`, value)
}

func TestSanitizeUsesDifferentMaskingForStructuredAndFreeFormSecrets(t *testing.T) {
	structured := Sanitize(map[string]any{"token": "sk-proj-abc123def456ghi789jkl012"})
	require.Equal(t, map[string]any{"token": "[REDACTED]"}, structured)

	freeForm := Sanitize(`OPENAI_API_KEY=sk-proj-abc123def456ghi789jkl012`)
	require.Equal(t, `OPENAI_API_KEY=sk-pro...l012`, freeForm)
}

func TestSanitizeRedactsQuotedEnvValuesWithSpaces(t *testing.T) {
	value := Sanitize(`PASSWORD="correct horse battery staple" AUTH_SECRET='another secret value'`)
	require.Equal(t, `PASSWORD="correc...aple" AUTH_SECRET='anothe...alue'`, value)
}

func TestSanitizeRedactsJSONStringFieldsWhenNotParsed(t *testing.T) {
	originalJSONUnmarshal := jsonUnmarshal
	jsonUnmarshal = func([]byte, any) error {
		return assert.AnError
	}
	defer func() {
		jsonUnmarshal = originalJSONUnmarshal
	}()

	value := Sanitize(`{"access_token":"eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.longtoken.here","name":"John"}`)
	require.Equal(t, `{"access_token": "eyJhbG...here","name":"John"}`, value)
}

func TestSanitizeRedactsTelegramToken(t *testing.T) {
	value := Sanitize("bot123456789:ABCDEfghij-KLMNopqrst_UVWXyz12345")
	require.Equal(t, "bot123456789:***", value)
}

func TestSanitizeRedactsPrivateKeyBlocks(t *testing.T) {
	value := Sanitize("-----BEGIN RSA PRIVATE KEY-----\nsecret\n-----END RSA PRIVATE KEY-----")
	require.Equal(t, "[REDACTED PRIVATE KEY]", value)
}

func TestSanitizeRedactsDatabaseConnectionCredentials(t *testing.T) {
	value := Sanitize("postgres://user:supersecret@localhost/db")
	require.Equal(t, "postgres://user:***@localhost/db", value)
}

func TestSanitizeDoesNotMatchURLCredentialsAcrossLines(t *testing.T) {
	value := strings.Join([]string{
		"Open · https://example.com:",
		"Browser result",
		"@navigation",
	}, "\n")

	require.Equal(t, value, Sanitize(value))
}

func TestSanitizeRedactsGenericURLCredentials(t *testing.T) {
	value := Sanitize("https://alice:supersecret@example.com/path ftp://bob:hunter2@example.com/files")
	require.Equal(t, "https://alice:***@example.com/path ftp://bob:***@example.com/files", value)
}

func TestSanitizeRedactsPhoneNumbers(t *testing.T) {
	value := Sanitize("Call +15551234567 or +442071838750 now")
	require.Equal(t, "Call +155****4567 or +442****8750 now", value)

	value = Sanitize("Call +2349167076428 now")
	require.Equal(t, "Call +234****6428 now", value)
}

func TestSanitizeRedactsShortPhoneNumbers(t *testing.T) {
	value := Sanitize("Code +1234567")
	require.Equal(t, "Code +1****67", value)
}

func TestSanitizeRedactsPIIClasses(t *testing.T) {
	value := Sanitize(strings.Join([]string{
		"Email jane.doe@example.com.",
		"SSN 123-45-6789.",
		"Card 4111 1111 1111 1111.",
		"IBAN GB82WEST12345698765432.",
		"Address 123 Main Street.",
	}, " "))

	require.Equal(t, "Email ja***@example.com. SSN ***-**-****. Card **** **** **** 1111. IBAN GB82WE...5432. Address [REDACTED ADDRESS].", value)
}

func TestNewRedactorWithOptions_DisablesPIIRedactionWithoutDisablingSecretRedaction(t *testing.T) {
	redactor := NewRedactorWithOptions(RedactorOptions{DisablePII: true})

	value := redactor.Sanitize("Email jane.doe@example.com TOKEN=example +15551234567")

	require.Equal(t, "Email jane.doe@example.com TOKEN=*** +15551234567", value)
}

func TestSanitizeDoesNotRedactInvalidCreditCardNumbers(t *testing.T) {
	value := Sanitize("Tracking number 4111 1111 1111 1112")

	require.Equal(t, "Tracking number 4111 1111 1111 1112", value)
}
