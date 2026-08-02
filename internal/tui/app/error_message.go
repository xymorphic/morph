package tui

import (
	"bytes"
	"encoding/json"

	"github.com/xymorphic/morph/pkg/str"
)

func getUserFacingErrorMessage(message string) string {
	messageValue := str.String(message)
	message = messageValue.Trim()
	if message == "" {
		return ""
	}

	if providerMessage := getProviderErrorMessage(message); providerMessage != "" {
		return "Model provider rejected the request: " + providerMessage
	}

	return message
}

func getProviderErrorMessage(message string) string {
	for index, char := range message {
		if char != '{' {
			continue
		}

		var payload map[string]any
		decoder := json.NewDecoder(bytes.NewBufferString(message[index:]))
		if err := decoder.Decode(&payload); err != nil {
			continue
		}
		if errorMessage := getErrorMessageFromPayload(payload); errorMessage != "" {
			return errorMessage
		}
	}

	return ""
}

func getErrorMessageFromPayload(payload map[string]any) string {
	if len(payload) == 0 {
		return ""
	}

	if value, ok := payload["error"]; ok {
		if message := getErrorMessageFromValue(value); message != "" {
			return message
		}
	}

	return getStringPayloadField(payload, "message")
}

func getErrorMessageFromValue(value any) string {
	switch typed := value.(type) {
	case string:
		typedValue := str.String(typed)
		return typedValue.Trim()
	case map[string]any:
		return getStringPayloadField(typed, "message")
	default:
		return ""
	}
}

func getStringPayloadField(payload map[string]any, field string) string {
	value, _ := payload[field].(string)
	valueText := str.String(value)
	return valueText.Trim()
}
