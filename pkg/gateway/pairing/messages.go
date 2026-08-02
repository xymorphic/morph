package pairing

import (
	"fmt"

	"github.com/xymorphic/morph/pkg/str"
)

func ChallengeMessage(challenge Challenge) string {
	codeValue := str.String(challenge.Code)
	code := codeValue.Trim()
	sourceValue := str.String(challenge.Request.Source)
	source := sourceValue.Trim()
	if source == "" {
		source = "gateway"
	}

	return fmt.Sprintf(
		"Pair this %s chat with Morph by running:\n\n```shell\nmorph gateway pairing approve %s %s\n```\n\nThis code expires soon.",
		source,
		source,
		code,
	)
}
