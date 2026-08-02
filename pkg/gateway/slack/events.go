package slack

import (
	"encoding/json"
	"errors"

	"github.com/xymorphic/morph/pkg/str"
)

const (
	EventTypeURLVerification = "url_verification"
	EventTypeCallback        = "event_callback"
	EventMessage             = "message"
	EventAppMention          = "app_mention"
	SocketTypeEventsAPI      = "events_api"
)

var ErrSlackChannelRequired = errors.New("slack channel id is required")

type EventsRequest struct {
	Type      string          `json:"type"`
	Token     string          `json:"token,omitempty"`
	Challenge string          `json:"challenge,omitempty"`
	TeamID    string          `json:"team_id,omitempty"`
	EventID   string          `json:"event_id,omitempty"`
	EventTime int64           `json:"event_time,omitempty"`
	Event     json.RawMessage `json:"event,omitempty"`
}

type Event struct {
	Type        string `json:"type"`
	Subtype     string `json:"subtype,omitempty"`
	Team        string `json:"team,omitempty"`
	Channel     string `json:"channel,omitempty"`
	ChannelType string `json:"channel_type,omitempty"`
	User        string `json:"user,omitempty"`
	Text        string `json:"text,omitempty"`
	TS          string `json:"ts,omitempty"`
	ThreadTS    string `json:"thread_ts,omitempty"`
	EventTS     string `json:"event_ts,omitempty"`
	BotID       string `json:"bot_id,omitempty"`
}

type SocketEnvelope struct {
	EnvelopeID string          `json:"envelope_id"`
	Type       string          `json:"type"`
	Payload    json.RawMessage `json:"payload"`
}

type SocketAck struct {
	EnvelopeID string `json:"envelope_id"`
}

type Target struct {
	TeamID          string
	ChannelID       string
	ThreadTS        string
	UserID          string
	ChannelType     string
	RecipientUserID string
	RecipientTeamID string
}

type InboundMessage struct {
	EventID    string
	TeamID     string
	ChannelID  string
	ThreadTS   string
	MessageTS  string
	Text       string
	SenderID   string
	Target     Target
	SocketID   string
	SocketType string
}

func DecodeEventsRequest(body []byte) (EventsRequest, error) {
	var req EventsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return EventsRequest{}, err
	}
	trimmedValueValue := str.String(req.Type)
	req.Type = trimmedValueValue.Trim()
	challengeValue := str.String(req.Challenge)
	req.Challenge = challengeValue.Trim()
	teamIDValue := str.String(req.TeamID)
	req.TeamID = teamIDValue.Trim()
	eventIDValue := str.String(req.EventID)
	req.EventID = eventIDValue.Trim()
	return req, nil
}

func NormalizeEventsRequest(req EventsRequest) (InboundMessage, bool, error) {
	trimmedValueValue2 := str.String(req.Type)
	if trimmedValueValue2.Trim() != EventTypeCallback {
		return InboundMessage{}, false, nil
	}
	var event Event
	if err := json.Unmarshal(req.Event, &event); err != nil {
		return InboundMessage{}, false, err
	}
	teamIDValue2 := str.String(req.TeamID)
	eventIDValue2 := str.String(req.EventID)
	inbound, ok, err := NormalizeEvent(teamIDValue2.Trim(), eventIDValue2.Trim(), event)
	if err != nil || !ok {
		return inbound, ok, err
	}

	return inbound, true, nil
}

func NormalizeSocketEnvelope(envelope SocketEnvelope) (InboundMessage, bool, error) {
	trimmedValueValue3 := str.String(envelope.Type)
	if trimmedValueValue3.Trim() != SocketTypeEventsAPI || len(envelope.Payload) == 0 {
		return InboundMessage{}, false, nil
	}

	var req EventsRequest
	if err := json.Unmarshal(envelope.Payload, &req); err != nil {
		return InboundMessage{}, false, err
	}
	inbound, ok, err := NormalizeEventsRequest(req)
	if err != nil || !ok {
		return inbound, ok, err
	}
	envelopeIDValue := str.String(envelope.EnvelopeID)
	inbound.SocketID = envelopeIDValue.Trim()
	trimmedValueValue4 := str.String(envelope.Type)
	inbound.SocketType = trimmedValueValue4.Trim()
	return inbound, true, nil
}

func NormalizeEvent(teamID string, eventID string, event Event) (InboundMessage, bool, error) {
	trimmedValueValue5 := str.String(event.Type)
	event.Type = trimmedValueValue5.Trim()
	if event.Type != EventMessage && event.Type != EventAppMention {
		return InboundMessage{}, false, nil
	}
	botIDValue := str.String(event.BotID)
	if botIDValue.Trim() != "" || isIgnoredMessageSubtype(event.Subtype) {
		return InboundMessage{}, false, nil
	}
	textValue := str.String(event.Text)
	text := textValue.Trim()
	if text == "" {
		return InboundMessage{}, false, nil
	}
	channelValue := str.String(event.Channel)
	channelID := channelValue.Trim()
	if channelID == "" {
		return InboundMessage{}, false, ErrSlackChannelRequired
	}
	if event.Type == EventMessage && !isDirectChannel(event.ChannelType) {
		return InboundMessage{}, false, nil
	}

	teamID = firstNonEmpty(teamID, event.Team)
	messageTS := firstNonEmpty(event.TS, event.EventTS)
	threadTS := firstNonEmpty(event.ThreadTS, messageTS)
	userValue := str.String(event.User)
	userID := userValue.Trim()
	channelTypeValue := str.String(event.ChannelType)
	channelType := channelTypeValue.Trim()
	target := Target{
		TeamID:      teamID,
		ChannelID:   channelID,
		ThreadTS:    threadTS,
		UserID:      userID,
		ChannelType: channelType,
	}
	if channelType == "im" {
		target.RecipientUserID = userID
		target.RecipientTeamID = teamID
	}
	eventIDValue3 := str.String(eventID)
	return InboundMessage{
		EventID:   eventIDValue3.Trim(),
		TeamID:    teamID,
		ChannelID: channelID,
		ThreadTS:  threadTS,
		MessageTS: messageTS,
		Text:      text,
		SenderID:  userID,
		Target:    target,
	}, true, nil
}

func isIgnoredMessageSubtype(subtype string) bool {
	subtypeValue := str.String(subtype)
	switch subtypeValue.Trim() {
	case "", "file_share", "thread_broadcast":
		return false
	default:
		return true
	}
}

func isDirectChannel(channelType string) bool {
	channelTypeValue2 := str.String(channelType)
	switch channelTypeValue2.Trim() {
	case "im", "mpim":
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		valueText := str.String(value)
		if valueText := valueText.Trim(); valueText != "" {
			return valueText
		}
	}

	return ""
}
