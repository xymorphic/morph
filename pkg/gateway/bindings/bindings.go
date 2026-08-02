package bindings

import (
	"errors"
	"net/url"
	"strings"

	"github.com/xymorphic/morph/pkg/str"
)

const (
	SourceGeneric  = "generic"
	SourceSlack    = "slack"
	SourceTelegram = "telegram"
)

type Key string

type Parts struct {
	Source         string
	AccountID      string
	ConversationID string
	ThreadID       string
}

func Generic(conversationID string) (Key, error) {
	return NewKey(Parts{
		Source:         SourceGeneric,
		ConversationID: conversationID,
	})
}

func Slack(teamID string, channelID string, threadID string) (Key, error) {
	return NewKey(Parts{
		Source:         SourceSlack,
		AccountID:      teamID,
		ConversationID: channelID,
		ThreadID:       threadID,
	})
}

func Telegram(chatID string, topicID string) (Key, error) {
	return NewKey(Parts{
		Source:         SourceTelegram,
		ConversationID: chatID,
		ThreadID:       topicID,
	})
}

func NewKey(parts Parts) (Key, error) {
	sourceValue := str.String(parts.Source)
	source := sourceValue.Normalized()
	accountIDValue := str.String(parts.AccountID)
	accountID := accountIDValue.Trim()
	conversationIDValue := str.String(parts.ConversationID)
	conversationID := conversationIDValue.Trim()
	threadIDValue := str.String(parts.ThreadID)
	threadID := threadIDValue.Trim()

	if source == "" {
		return "", errors.New("gateway binding source is required")
	}
	if conversationID == "" {
		return "", errors.New("gateway binding conversation id is required")
	}
	if source == SourceSlack && accountID == "" {
		return "", errors.New("slack team id is required")
	}

	return Key(strings.Join([]string{
		escape(source),
		escape(accountID),
		escape(conversationID),
		escape(threadID),
	}, ":")), nil
}

func (k Key) String() string {
	return string(k)
}

func ParseKey(key Key) (Parts, error) {
	rawParts := strings.Split(key.String(), ":")
	if len(rawParts) != 4 {
		return Parts{}, errors.New("gateway binding key must have four parts")
	}

	source, err := unescape(rawParts[0])
	if err != nil {
		return Parts{}, err
	}
	accountID, err := unescape(rawParts[1])
	if err != nil {
		return Parts{}, err
	}
	conversationID, err := unescape(rawParts[2])
	if err != nil {
		return Parts{}, err
	}
	threadID, err := unescape(rawParts[3])
	if err != nil {
		return Parts{}, err
	}

	parts := Parts{
		Source:         strings.ToLower(source),
		AccountID:      accountID,
		ConversationID: conversationID,
		ThreadID:       threadID,
	}
	if _, err := NewKey(parts); err != nil {
		return Parts{}, err
	}

	return parts, nil
}

func escape(value string) string {
	return url.QueryEscape(value)
}

func unescape(value string) (string, error) {
	unescaped, err := url.QueryUnescape(value)
	if err != nil {
		return "", errors.New("gateway binding key is invalid")
	}
	unescapedValue := str.String(unescaped)
	return unescapedValue.Trim(), nil
}
