package tui

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	browserdomain "github.com/wandxy/morph/internal/browser"
	"github.com/wandxy/morph/internal/trace"
)

func TestTranscriptRenderCache_ReusesStableCellsAndInvalidatesChangedInputs(t *testing.T) {
	cache := newTranscriptRenderCache(16)
	cells := []transcriptCell{
		assistantTranscriptCell{text: "**cached** response"},
		systemTranscriptCell{text: "stable"},
	}

	first := renderTranscriptCellsWithFrameAndCache(cells, 80, 0, cache)
	require.NotEmpty(t, first)
	require.Equal(t, uint64(2), cache.misses)
	require.Zero(t, cache.hits)

	second := renderTranscriptCellsWithFrameAndCache(cells, 80, 1, cache)
	require.Equal(t, first, second)
	require.Equal(t, uint64(2), cache.hits)

	changedWidth := renderTranscriptCellsWithFrameAndCache(cells, 60, 1, cache)
	require.NotEmpty(t, changedWidth)
	require.Equal(t, uint64(4), cache.misses)

	cells[1] = systemTranscriptCell{text: "changed"}
	changedContent := renderTranscriptCellsWithFrameAndCache(cells, 60, 1, cache)
	require.Contains(t, stripANSI(changedContent), "changed")
	require.Equal(t, uint64(5), cache.misses)
}

func TestTranscriptRenderCache_RendersOnlyAnimatedToolGroupAcrossFrames(t *testing.T) {
	cache := newTranscriptRenderCache(16)
	cells := []transcriptCell{
		assistantTranscriptCell{text: "stable"},
		toolTranscriptCell{id: "call_1", action: "Browser", detail: "snapshot", startedAt: time.Now()},
	}

	first := renderTranscriptCellsWithFrameAndCache(cells, 80, 0, cache)
	hits := cache.hits
	misses := cache.misses
	second := renderTranscriptCellsWithFrameAndCache(cells, 80, 1, cache)

	require.NotEqual(t, first, second)
	require.Equal(t, hits+1, cache.hits)
	require.Equal(t, misses, cache.misses)
	require.Equal(t, 1, cache.len())
}

func TestTranscriptRenderCache_InvalidatesArtifactRetentionLabel(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	cache := newTranscriptRenderCache(16)
	cell := toolTranscriptCell{
		id:          "call_1",
		action:      "Browser",
		detail:      "screenshot:Full page",
		completed:   true,
		completedAt: now,
		hasArtifact: true,
		artifact: browserArtifact{
			Handle:    "artifact_1",
			Kind:      browserdomain.ArtifactScreenshot,
			Name:      "screenshot.png",
			Size:      1024,
			ExpiresAt: now.Add(2 * time.Hour),
		},
	}

	first := defaultTranscriptRenderer.RenderCells(
		[]transcriptCell{cell},
		transcriptRenderContext{Width: 80, Now: now, Cache: cache},
	)
	second := defaultTranscriptRenderer.RenderCells(
		[]transcriptCell{cell},
		transcriptRenderContext{Width: 80, Now: now.Add(90 * time.Minute), Cache: cache},
	)

	require.Contains(t, stripANSI(first), "expires in 2h")
	require.Contains(t, stripANSI(second), "expires in 30m")
	require.Equal(t, uint64(2), cache.misses)
}

func TestTranscriptRenderCache_InvalidatesPlanStateContent(t *testing.T) {
	state := &trace.PlanToolState{Operation: trace.PlanToolOperationRead, TotalCount: 1}
	group := completedToolTranscriptGroup("Plan", toolTranscriptDetail{
		id:        "call_1",
		planState: state,
		completed: true,
	})
	cache := newTranscriptRenderCache(16)
	ctx := transcriptRenderContext{Width: 80, Now: time.Now(), Cache: cache}

	first := strings.Join(renderCachedToolTranscriptGroupLines(group, ctx), "\n")
	state.TotalCount = 2
	second := strings.Join(renderCachedToolTranscriptGroupLines(group, ctx), "\n")

	require.NotEqual(t, first, second)
	require.Contains(t, stripANSI(first), "Found 1 task")
	require.Contains(t, stripANSI(second), "Found 2 tasks")
	require.Equal(t, uint64(2), cache.misses)
}

func TestTranscriptRenderCache_InvalidatesNestedProcessStateContent(t *testing.T) {
	exitCode := 0
	state := &trace.ProcessToolState{
		Operation: trace.ProcessToolOperationStatus,
		ProcessID: "proc_1",
		Status:    "exited",
		ExitCode:  &exitCode,
	}
	group := completedToolTranscriptGroup("Process", toolTranscriptDetail{
		id:           "call_1",
		processState: state,
		completed:    true,
	})
	cache := newTranscriptRenderCache(16)
	ctx := transcriptRenderContext{Width: 80, Now: time.Now(), Cache: cache}

	first := strings.Join(renderCachedToolTranscriptGroupLines(group, ctx), "\n")
	exitCode = 1
	second := strings.Join(renderCachedToolTranscriptGroupLines(group, ctx), "\n")

	require.NotEqual(t, first, second)
	require.Contains(t, stripANSI(first), "exit 0")
	require.Contains(t, stripANSI(second), "exit 1")
	require.Equal(t, uint64(2), cache.misses)
}

func TestTranscriptRenderCache_BypassesToolGroupCacheWhenIdentityEncodingFails(t *testing.T) {
	originalEncoder := encodeToolTranscriptGroupRenderIdentity
	encodeToolTranscriptGroupRenderIdentity = func(any) ([]byte, error) {
		return nil, errors.New("encode failed")
	}
	t.Cleanup(func() {
		encodeToolTranscriptGroupRenderIdentity = originalEncoder
	})

	group := completedToolTranscriptGroup("Plan", toolTranscriptDetail{
		id:        "call_1",
		planState: &trace.PlanToolState{Operation: trace.PlanToolOperationRead, TotalCount: 1},
		completed: true,
	})
	cache := newTranscriptRenderCache(16)

	rendered := strings.Join(renderCachedToolTranscriptGroupLines(group, transcriptRenderContext{
		Width: 80,
		Now:   time.Now(),
		Cache: cache,
	}), "\n")

	require.Contains(t, stripANSI(rendered), "Found 1 task")
	require.Zero(t, cache.len())
	require.Zero(t, cache.hits)
	require.Zero(t, cache.misses)
}

func TestTranscriptRenderCache_EvictsLeastRecentlyUsedEntryAndClears(t *testing.T) {
	cache := newTranscriptRenderCache(2)
	first := transcriptRenderCacheKey{identity: getTranscriptRenderIdentity("first")}
	second := transcriptRenderCacheKey{identity: getTranscriptRenderIdentity("second")}
	third := transcriptRenderCacheKey{identity: getTranscriptRenderIdentity("third")}
	cache.set(first, []string{"1"})
	cache.set(second, []string{"2"})
	cache.set(first, []string{"updated"})
	require.Equal(t, 2, cache.len())
	value, ok := cache.get(first)
	require.True(t, ok)
	require.Equal(t, []string{"updated"}, value)
	cache.set(third, []string{"3"})

	_, ok = cache.get(second)
	require.False(t, ok)
	require.Equal(t, 2, cache.len())

	cache.clear()
	require.Zero(t, cache.len())
	require.Zero(t, cache.hits)
	require.Zero(t, cache.misses)
}

func TestTranscriptRenderCache_UsesDefaultCapacity(t *testing.T) {
	cache := newTranscriptRenderCache(0)

	require.Equal(t, defaultTranscriptRenderCacheCapacity, cache.capacity)
}

func TestTranscriptRenderCache_SingleCellIdentityTypesContainNoPointers(t *testing.T) {
	cells := []transcriptCell{
		userTranscriptCell{},
		assistantTranscriptCell{},
		thinkingTranscriptCell{},
		thoughtTranscriptCell{},
		safetyTranscriptCell{},
		errorTranscriptCell{},
		systemTranscriptCell{},
		permissionApprovalTranscriptCell{},
		manualCompactionTranscriptCell{},
	}

	for _, cell := range cells {
		cellType := reflect.TypeOf(cell)
		t.Run(cellType.Name(), func(t *testing.T) {
			require.True(t, isTranscriptCellIdentityCacheable(cell))
			path, found := getPointerFieldPath(cellType, nil)
			require.False(t, found, "single-cell cache identity contains pointer field %s", path)
		})
	}
	require.False(t, isTranscriptCellIdentityCacheable(toolTranscriptCell{}))
}

func TestTranscriptRenderCache_MatchesUncachedRenderer(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	exitCode := 0
	cells := []transcriptCell{
		userTranscriptCell{text: "Show **browser** state"},
		assistantTranscriptCell{text: "Opening the page.", duration: 2 * time.Second},
		thinkingTranscriptCell{summary: "Inspecting the available controls.", startedAt: now},
		thoughtTranscriptCell{duration: 3 * time.Second},
		safetyTranscriptCell{action: "blocked", findingIDs: []string{"prompt_exfiltration"}},
		errorTranscriptCell{message: "page unavailable"},
		systemTranscriptCell{text: "Session restored"},
		manualCompactionTranscriptCell{state: manualCompactionState{Status: "succeeded"}},
		toolTranscriptCell{
			id:          "call_1",
			action:      "Browser",
			detail:      "screenshot:Full page",
			startedAt:   now,
			completedAt: now.Add(time.Second),
			completed:   true,
			hasArtifact: true,
			artifact: browserArtifact{
				Handle:    "artifact_1",
				Kind:      browserdomain.ArtifactScreenshot,
				Name:      "screenshot.png",
				Size:      1024,
				ExpiresAt: now.Add(time.Hour),
			},
		},
		toolTranscriptCell{
			id:        "call_2",
			action:    "Plan",
			planState: &trace.PlanToolState{Operation: trace.PlanToolOperationRead, TotalCount: 2},
			completed: true,
		},
		toolTranscriptCell{
			id:     "call_3",
			action: "Process",
			processState: &trace.ProcessToolState{
				Operation: trace.ProcessToolOperationStatus,
				ProcessID: "proc_1",
				Status:    "exited",
				ExitCode:  &exitCode,
			},
			completed: true,
		},
		permissionApprovalTranscriptCell{message: permissionApprovalMsg{
			RequestID: "approval_1",
			Status:    "pending",
			Summary:   "browser · read network",
			Reason:    "Opening this page requires network access",
			ExpiresAt: now.Add(2 * time.Minute),
		}},
	}
	for _, width := range []int{48, 90, 140} {
		ctx := transcriptRenderContext{Width: width, Frame: 1, Now: now}
		uncached := defaultTranscriptRenderer.RenderCells(cells, ctx)
		ctx.Cache = newTranscriptRenderCache(64)
		cached := defaultTranscriptRenderer.RenderCells(cells, ctx)
		require.Equal(t, uncached, cached)
	}
}

func TestTranscriptRenderCache_CachesPaddingInsideLines(t *testing.T) {
	require.Equal(
		t,
		[]string{"  first", "", "  second"},
		getPaddedTranscriptLines("first\n\nsecond", 2),
	)

	cache := newTranscriptRenderCache(8)
	cells := []transcriptCell{
		safetyTranscriptCell{action: "blocked"},
	}

	rendered := renderTranscriptCellsWithPadding(cells, 80, 2, 0, cache)

	require.Equal(t, "  Safety: blocked", stripANSI(rendered))
	require.Equal(t, uint64(1), cache.misses)
	key := getTranscriptCellRenderCacheKey(cells[0], transcriptRenderContext{Width: 80, Padding: 2})
	lines, ok := cache.get(key)
	require.True(t, ok)
	require.Equal(t, []string{"  Safety: blocked"}, stripANSILines(lines))
}

func TestGetPaddedTranscriptLinesHandlesEmptyAndUnpaddedContent(t *testing.T) {
	require.Nil(t, getPaddedTranscriptLines("", 2))
	require.Equal(t, []string{"first", "second"}, getPaddedTranscriptLines("first\nsecond", 0))
}

func stripANSILines(lines []string) []string {
	plain := make([]string, len(lines))
	for index, line := range lines {
		plain[index] = stripANSI(line)
	}

	return plain
}

func completedToolTranscriptGroup(action string, detail toolTranscriptDetail) toolTranscriptGroup {
	return toolTranscriptGroup{
		action:       action,
		details:      []toolTranscriptDetail{detail},
		seenIDs:      map[string]bool{detail.id: true},
		completedIDs: map[string]bool{detail.id: true},
	}
}

func getTranscriptCellRenderCacheKeyForModel(runModel *model, cell transcriptCell) transcriptRenderCacheKey {
	width := max(runModel.transcript.Width(), runModel.getMainPaneWidth())
	contentWidth := getPanelContentWidth(width)
	if contentWidth <= 0 {
		contentWidth = max(width, 1)
	}

	return getTranscriptCellRenderCacheKey(cell, transcriptRenderContext{
		Width:   contentWidth,
		Padding: getPanelHorizontalPadding(width),
	})
}

func getPointerFieldPath(valueType reflect.Type, visited map[reflect.Type]bool) (string, bool) {
	if valueType == reflect.TypeOf(time.Time{}) {
		return "", false
	}
	if visited == nil {
		visited = make(map[reflect.Type]bool)
	}
	if visited[valueType] {
		return "", false
	}
	visited[valueType] = true

	switch valueType.Kind() {
	case reflect.Pointer, reflect.UnsafePointer, reflect.Interface, reflect.Func, reflect.Chan:
		return valueType.String(), true
	case reflect.Array, reflect.Slice:
		return getPointerFieldPath(valueType.Elem(), visited)
	case reflect.Map:
		if path, found := getPointerFieldPath(valueType.Key(), visited); found {
			return "map key." + path, true
		}
		if path, found := getPointerFieldPath(valueType.Elem(), visited); found {
			return "map value." + path, true
		}
	case reflect.Struct:
		for index := 0; index < valueType.NumField(); index++ {
			field := valueType.Field(index)
			if path, found := getPointerFieldPath(field.Type, visited); found {
				return field.Name + "." + path, true
			}
		}
	}

	return "", false
}
