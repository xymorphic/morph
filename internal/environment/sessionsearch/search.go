package sessionsearch

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xymorphic/morph/internal/constants"
	storage "github.com/xymorphic/morph/internal/state/core"
	statemanager "github.com/xymorphic/morph/internal/state/manager"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/str"
)

const (
	defaultSessionSearchMaxResults = constants.DefaultSessionSearchMaxResults
	maxSessionSearchResults        = constants.MaxSessionSearchResults
	maxSessionMatchedMessages      = constants.MaxSessionMatchedMessages
	sessionSearchSnippetRunes      = constants.SessionSearchSnippetRunes
)

// Search finds session messages matching the supplied query.
func Search(
	ctx context.Context,
	manager *statemanager.Manager,
	req SessionSearchRequest,
) ([]SessionSearchResult, error) {
	if manager == nil {
		return nil, errors.New("state manager is required")
	}

	sessionIDValue := str.String(req.SessionID)
	sessionID := sessionIDValue.Trim()
	ignoreSessionIDValue := str.String(req.IgnoreSessionID)
	ignoreSessionID := ignoreSessionIDValue.Trim()
	queryValue := str.String(req.Query)
	query := queryValue.Trim()
	if query == "" {
		return nil, errors.New("query is required")
	}
	roleValue := str.String(req.Role)
	role := roleValue.Normalized()
	toolNameValue := str.String(req.ToolName)
	toolName := toolNameValue.Normalized()
	limit := clampSearchResults(req.MaxResults)

	results, err := manager.SearchMessages(ctx, sessionID, storage.SearchMessageOptions{
		IgnoreSessionID:       ignoreSessionID,
		MaxMessagesPerSession: maxSessionMatchedMessages,
		MaxSessions:           limit,
		Query:                 query,
		Role:                  morphmsg.Role(role),
		ToolName:              toolName,
	})
	if err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return nil, nil
	}

	groupedResults := make([]SessionSearchResult, 0, len(results))
	for _, result := range results {
		session, found, err := manager.Get(ctx, result.SessionID, storage.SessionGetOptions{})
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}

		summary, _, err := manager.GetSummary(ctx, result.SessionID)
		if err != nil {
			return nil, err
		}
		sessionSummaryValue := str.String(summary.SessionSummary)
		group := SessionSearchResult{
			SessionID:      result.SessionID,
			SessionCreated: formatSearchTime(session.CreatedAt),
			SessionUpdated: formatSearchTime(session.UpdatedAt),
			MatchCount:     result.MatchCount,
			SessionSummary: sessionSummaryValue.Trim(),
			Messages:       make([]SessionSearchMessageHit, 0, len(result.Messages)),
		}

		for _, hit := range result.Messages {
			matchedTextValue := str.String(hit.MatchedText)
			if matchedTextValue.Trim() == "" {
				continue
			}

			matchIndex, matchLen := getCaseInsensitiveMatchIndex(hit.MatchedText, query)
			snippet := getSnippetAround(hit.MatchedText, 0, 0, sessionSearchSnippetRunes)
			if matchIndex >= 0 {
				snippet = getSnippetAround(hit.MatchedText, matchIndex, matchLen, sessionSearchSnippetRunes)
			}

			group.Messages = append(group.Messages, SessionSearchMessageHit{
				MessageID:     hit.Message.ID,
				Role:          string(hit.Message.Role),
				ToolName:      hit.MatchedToolName,
				CreatedAt:     formatSearchTime(hit.Message.CreatedAt),
				Snippet:       snippet,
				FullTextBytes: len(hit.MatchedText),
				MatchIndex:    matchIndex,
			})
		}

		groupedResults = append(groupedResults, group)
	}

	return groupedResults, nil
}

func formatSearchTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}

	return value.UTC().Format("2006-01-02T15:04:05Z07:00")
}

func clampSearchResults(value int) int {
	if value <= 0 {
		return defaultSessionSearchMaxResults
	}
	if value > maxSessionSearchResults {
		return maxSessionSearchResults
	}

	return value
}

func getCaseInsensitiveMatchIndex(text string, query string) (int, int) {
	queryValue2 := str.String(query)
	query = queryValue2.Trim()
	if query == "" {
		return -1, 0
	}

	queryRunes := utf8.RuneCountInString(query)

	offsets := make([]int, 0, utf8.RuneCountInString(text)+1)
	for offset := range text {
		offsets = append(offsets, offset)
	}
	offsets = append(offsets, len(text))

	for i := 0; i+queryRunes < len(offsets); i++ {
		start := offsets[i]
		end := offsets[i+queryRunes]
		if strings.EqualFold(text[start:end], query) {
			return start, end - start
		}
	}

	return -1, 0
}

func getSnippetAround(text string, matchIndex int, matchLen int, maxRunes int) string {
	if text == "" || maxRunes <= 0 {
		return ""
	}

	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "")
	}

	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}

	startRune := utf8.RuneCountInString(text[:matchIndex])

	windowStart := max(startRune-(maxRunes/2), 0)
	windowEnd := min(windowStart+maxRunes, len(runes))
	if windowEnd-windowStart < maxRunes && windowEnd == len(runes) {
		windowStart = max(windowEnd-maxRunes, 0)
	}

	snippet := string(runes[windowStart:windowEnd])
	if windowStart > 0 {
		snippet = "..." + snippet
	}
	if windowEnd < len(runes) {
		snippet += "..."
	}

	return snippet
}
