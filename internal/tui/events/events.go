package events

import rpcclient "github.com/xymorphic/morph/internal/rpc/client"

// Event is implemented by messages that flow through the TUI update loop.
type Event interface {
	TUIEvent()
}

// ViewportResizedEvent reports the terminal viewport dimensions after a resize.
type ViewportResizedEvent struct {
	Width  int
	Height int
}

// SubmitComposerEvent requests submission of the current composer text.
type SubmitComposerEvent struct{}

// CopyTranscriptEvent requests copying the rendered transcript.
type CopyTranscriptEvent struct{}

// JumpTranscriptToBottomEvent requests scrolling the transcript to the latest line.
type JumpTranscriptToBottomEvent struct{}

// ShowPreviousPromptEvent requests the previous prompt from composer history.
type ShowPreviousPromptEvent struct{}

// ShowNextPromptEvent requests the next prompt from composer history.
type ShowNextPromptEvent struct{}

// InsertInputNewlineEvent requests inserting a newline into the composer.
type InsertInputNewlineEvent struct{}

// DeleteInputLineEvent requests deleting the current composer line.
type DeleteInputLineEvent struct{}

// ApplyTUIMessageEvent wraps an incoming Bubble Tea message for app handling.
type ApplyTUIMessageEvent struct {
	Message any
}

// HydrateTimelineEvent replaces the visible transcript with a loaded session timeline.
type HydrateTimelineEvent struct {
	Timeline rpcclient.SessionTimeline
}

func (ViewportResizedEvent) TUIEvent()        {}
func (SubmitComposerEvent) TUIEvent()         {}
func (CopyTranscriptEvent) TUIEvent()         {}
func (JumpTranscriptToBottomEvent) TUIEvent() {}
func (ShowPreviousPromptEvent) TUIEvent()     {}
func (ShowNextPromptEvent) TUIEvent()         {}
func (InsertInputNewlineEvent) TUIEvent()     {}
func (DeleteInputLineEvent) TUIEvent()        {}
func (ApplyTUIMessageEvent) TUIEvent()        {}
func (HydrateTimelineEvent) TUIEvent()        {}
