package telegram

import (
	"crypto/subtle"
	"errors"

	"github.com/xymorphic/morph/pkg/str"
)

const WebhookSecretHeader = "X-Telegram-Bot-Api-Secret-Token"

var ErrWebhookSecretMismatch = errors.New("telegram webhook secret mismatch")

func CheckWebhookSecret(header string, secret string) error {
	secretValue := str.String(secret)
	secret = secretValue.Trim()
	if secret == "" {
		return nil
	}
	headerValue := str.String(header)
	header = headerValue.Trim()
	if header == "" {
		return ErrWebhookSecretMismatch
	}

	if subtle.ConstantTimeCompare([]byte(header), []byte(secret)) != 1 {
		return ErrWebhookSecretMismatch
	}

	return nil
}
