package memory

import (
	"github.com/xymorphic/morph/internal/agent/runcontext"
	"github.com/xymorphic/morph/pkg/str"
)

const (
	MemoryMetadataSourceSessionID = "source_session_id"
	MemoryMetadataTrigger         = "source_trigger"
)

// ApplyRunProvenance applies run provenance.
func ApplyRunProvenance(item MemoryItem, runCtx runcontext.Context, trigger string) MemoryItem {
	runCtx, err := runCtx.Normalize()
	if err != nil {
		return item
	}

	item = item.Clone()
	if item.Metadata == nil {
		item.Metadata = make(map[string]string)
	}

	setMetadata(item.Metadata, MemoryMetadataSourceSessionID, runCtx.Session.PublicID)
	for index := range item.SourceLinks {
		fillSourceLinkProvenance(&item.SourceLinks[index], runCtx, trigger)
	}

	if !hasSourceLinkProvenance(item.SourceLinks) {
		link := SourceLink{}
		fillSourceLinkProvenance(&link, runCtx, trigger)
		item.SourceLinks = append(item.SourceLinks, link)
	}

	return item
}

// HasSourceProvenance reports whether an item has source-session metadata or a source link.
func HasSourceProvenance(item MemoryItem) bool {
	if hasSourceLinkProvenance(item.SourceLinks) {
		return true
	}

	metadataValue := str.String(item.Metadata[MemoryMetadataSourceSessionID])
	return metadataValue.Trim() != ""
}

func setMetadata(metadata map[string]string, key string, value string) {
	valueText := str.String(value)
	if valueText := valueText.Trim(); valueText != "" {
		metadata[key] = valueText
	}
}

func hasSourceLinkProvenance(links []SourceLink) bool {
	for _, link := range links {
		sessionIDValue := str.String(link.SessionID)
		summaryIDValue := str.String(link.SummaryID)
		if sessionIDValue.Trim() != "" ||
			len(link.MessageIDs) > 0 ||
			len(link.Offsets) > 0 ||
			summaryIDValue.Trim() != "" {
			return true
		}
	}

	return false
}

func getRunChildSessionID(runCtx runcontext.Context) string {
	parentSessionIDValue := str.String(runCtx.Lineage.ParentSessionID)
	if parentSessionIDValue.Trim() == "" {
		return ""
	}
	childSessionIDValue := str.String(runCtx.Lineage.ChildSessionID)
	if childSessionIDValue.Trim() != "" {
		return runCtx.Lineage.ChildSessionID
	}

	return runCtx.Session.EffectiveID
}

func fillSourceLinkProvenance(link *SourceLink, runCtx runcontext.Context, trigger string) {
	if link == nil {
		return
	}

	if link.SessionID == "" {
		link.SessionID = runCtx.Session.PublicID
	}
	if link.SourceProfile == "" {
		link.SourceProfile = runCtx.ProfileName
	}
	if link.SourcePersonality == "" {
		link.SourcePersonality = runCtx.Personality.Name
	}
	if link.ParentSessionID == "" {
		link.ParentSessionID = runCtx.Lineage.ParentSessionID
	}
	if link.ChildSessionID == "" {
		link.ChildSessionID = getRunChildSessionID(runCtx)
	}
	if link.RunID == "" {
		link.RunID = runCtx.Lineage.RunID
	}
	if link.StateMode == "" {
		link.StateMode = runCtx.State.Mode
	}
	if link.SourceTrigger == "" {
		triggerValue := str.String(trigger)
		link.SourceTrigger = triggerValue.Trim()
	}
}
