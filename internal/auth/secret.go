package auth

const redactedSecret = "[REDACTED]"

type Secret string

func NewSecret(value string) Secret {
	return Secret(value)
}

func (s Secret) Reveal() string {
	return string(s)
}

func (Secret) String() string {
	return redactedSecret
}

func (Secret) GoString() string {
	return redactedSecret
}
