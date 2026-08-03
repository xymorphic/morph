package tui

import (
	"container/list"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/internal/trace"
)

const defaultTranscriptRenderCacheCapacity = 1024

var encodeToolTranscriptGroupRenderIdentity = json.Marshal

type transcriptRenderCacheKey struct {
	identity [sha256.Size]byte
	width    int
	padding  int
	dynamic  string
}

type transcriptRenderCacheEntry struct {
	key   transcriptRenderCacheKey
	lines []string
}

type transcriptRenderCache struct {
	capacity int
	entries  map[transcriptRenderCacheKey]*list.Element
	order    *list.List
	hits     uint64
	misses   uint64
}

type toolTranscriptGroupRenderIdentity struct {
	Action           string
	Details          []toolTranscriptDetailRenderIdentity
	SeenIDs          map[string]bool
	CompletedIDs     map[string]bool
	TerminalStatuses map[string]toolTranscriptTerminalStatus
	Completed        bool
	TerminalStatus   toolTranscriptTerminalStatus
}

type toolTranscriptDetailRenderIdentity struct {
	ID                   string
	Text                 string
	PlanState            *trace.PlanToolState
	ProcessState         *trace.ProcessToolState
	StartedAt            time.Time
	CompletedAt          time.Time
	Completed            bool
	TerminalStatus       toolTranscriptTerminalStatus
	Failure              string
	Artifact             browserArtifact
	ArtifactToken        string
	HasArtifact          bool
	ArtifactStatus       string
	Mode                 string
	ExecutionDuration    time.Duration
	ApprovalWaitDuration time.Duration
}

func newTranscriptRenderCache(capacity int) *transcriptRenderCache {
	if capacity <= 0 {
		capacity = defaultTranscriptRenderCacheCapacity
	}

	return &transcriptRenderCache{
		capacity: capacity,
		entries:  make(map[transcriptRenderCacheKey]*list.Element, capacity),
		order:    list.New(),
	}
}

func (cache *transcriptRenderCache) get(key transcriptRenderCacheKey) ([]string, bool) {
	element, ok := cache.entries[key]
	if !ok {
		cache.misses++
		return nil, false
	}

	cache.hits++
	cache.order.MoveToFront(element)
	return element.Value.(transcriptRenderCacheEntry).lines, true
}

func (cache *transcriptRenderCache) set(key transcriptRenderCacheKey, lines []string) {
	if element, ok := cache.entries[key]; ok {
		element.Value = transcriptRenderCacheEntry{key: key, lines: lines}
		cache.order.MoveToFront(element)
		return
	}

	element := cache.order.PushFront(transcriptRenderCacheEntry{key: key, lines: lines})
	cache.entries[key] = element
	if cache.order.Len() <= cache.capacity {
		return
	}

	oldest := cache.order.Back()
	cache.order.Remove(oldest)
	delete(cache.entries, oldest.Value.(transcriptRenderCacheEntry).key)
}

func (cache *transcriptRenderCache) clear() {
	clear(cache.entries)
	cache.order.Init()
	cache.hits = 0
	cache.misses = 0
}

func (cache *transcriptRenderCache) len() int {
	return cache.order.Len()
}

func getTranscriptCellRenderCacheKey(cell transcriptCell, ctx transcriptRenderContext) transcriptRenderCacheKey {
	return transcriptRenderCacheKey{
		identity: getTranscriptRenderIdentity(cell),
		width:    ctx.Width,
		padding:  ctx.Padding,
		dynamic:  getTranscriptCellDynamicRenderValue(cell, ctx),
	}
}

func getToolTranscriptGroupRenderCacheKey(
	group toolTranscriptGroup,
	ctx transcriptRenderContext,
) (transcriptRenderCacheKey, bool) {
	identity, ok := getToolTranscriptGroupRenderIdentity(group)
	if !ok {
		return transcriptRenderCacheKey{}, false
	}

	dynamic := make([]string, 0, len(group.details))
	for _, detail := range group.details {
		if detail.hasArtifact {
			dynamic = append(dynamic, formatBrowserArtifactRetention(detail.artifact.ExpiresAt, ctx.Now))
		}
	}

	return transcriptRenderCacheKey{
		identity: identity,
		width:    ctx.Width,
		padding:  ctx.Padding,
		dynamic:  strings.Join(dynamic, "\x00"),
	}, true
}

func getToolTranscriptGroupRenderIdentity(group toolTranscriptGroup) ([sha256.Size]byte, bool) {
	details := make([]toolTranscriptDetailRenderIdentity, len(group.details))
	for index, detail := range group.details {
		details[index] = toolTranscriptDetailRenderIdentity{
			ID:                   detail.id,
			Text:                 detail.text,
			PlanState:            detail.planState,
			ProcessState:         detail.processState,
			StartedAt:            detail.startedAt,
			CompletedAt:          detail.completedAt,
			Completed:            detail.completed,
			TerminalStatus:       detail.terminalStatus,
			Failure:              detail.failure,
			Artifact:             detail.artifact,
			ArtifactToken:        detail.artifact.Token,
			HasArtifact:          detail.hasArtifact,
			ArtifactStatus:       detail.artifactStatus,
			Mode:                 detail.mode,
			ExecutionDuration:    detail.executionDuration,
			ApprovalWaitDuration: detail.approvalWaitDuration,
		}
	}

	encoded, err := encodeToolTranscriptGroupRenderIdentity(toolTranscriptGroupRenderIdentity{
		Action:           group.action,
		Details:          details,
		SeenIDs:          group.seenIDs,
		CompletedIDs:     group.completedIDs,
		TerminalStatuses: group.terminalStatuses,
		Completed:        group.completed,
		TerminalStatus:   group.terminalStatus,
	})
	if err != nil {
		return [sha256.Size]byte{}, false
	}

	return sha256.Sum256(encoded), true
}

func getTranscriptRenderIdentity(value any) [sha256.Size]byte {
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%T:%#v", value, value)
	var identity [sha256.Size]byte
	copy(identity[:], hash.Sum(nil))
	return identity
}

func getTranscriptCellDynamicRenderValue(cell transcriptCell, ctx transcriptRenderContext) string {
	switch value := cell.(type) {
	case permissionApprovalTranscriptCell:
		if value.message.Status == string(permissions.ApprovalPending) {
			return permissions.GetApprovalExpiryText(value.message.ExpiresAt, ctx.Now)
		}
	}

	return ""
}

func isTranscriptCellIdentityCacheable(cell transcriptCell) bool {
	switch cell.(type) {
	case userTranscriptCell,
		assistantTranscriptCell,
		thinkingTranscriptCell,
		thoughtTranscriptCell,
		safetyTranscriptCell,
		errorTranscriptCell,
		systemTranscriptCell,
		permissionApprovalTranscriptCell,
		manualCompactionTranscriptCell:
		return true
	default:
		return false
	}
}

func isTranscriptCellFrameAnimated(cell transcriptCell) bool {
	switch value := cell.(type) {
	case manualCompactionTranscriptCell:
		return value.state.isInProgress()
	default:
		return false
	}
}

func isToolTranscriptGroupFrameAnimated(group toolTranscriptGroup) bool {
	for _, detail := range group.details {
		if !detail.completed && detail.terminalStatus == "" {
			return true
		}
	}

	return false
}
