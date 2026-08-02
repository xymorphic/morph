package core

import (
	"errors"

	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/str"
)

const (
	MessageOrderAsc  = "asc"
	MessageOrderDesc = "desc"
)

// MessageQueryOptions controls message listing and counting filters.
type MessageQueryOptions struct {
	Limit  int
	Name   string
	Order  string
	Offset int
	Role   morphmsg.Role
}

// MessageRecord pairs a message with its sequence offset.
type MessageRecord struct {
	Offset  int
	Message morphmsg.Message
}

// NormalizeMessageQueryOrder validates and canonicalizes message query order.
func NormalizeMessageQueryOrder(order string) (string, error) {
	orderValue := str.String(order)
	switch orderValue.Normalized() {
	case "", MessageOrderAsc:
		return MessageOrderAsc, nil
	case MessageOrderDesc:
		return MessageOrderDesc, nil
	default:
		return "", errors.New("message order must be asc or desc")
	}
}
