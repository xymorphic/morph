package memorywrite

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/xymorphic/morph/internal/agent/runcontext"
	envtypes "github.com/xymorphic/morph/internal/environment/types"
	"github.com/xymorphic/morph/internal/guardrails"
	"github.com/xymorphic/morph/internal/instructions"
	"github.com/xymorphic/morph/internal/memory"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/internal/tools"
	"github.com/xymorphic/morph/internal/tools/common"
	"github.com/xymorphic/morph/internal/trace"
	"github.com/xymorphic/morph/pkg/str"
)

type sourceLinkInput struct {
	SessionID         string `json:"session_id,omitempty"`
	MessageIDs        []uint `json:"message_ids,omitempty"`
	Offsets           []int  `json:"offsets,omitempty"`
	CreatedBy         string `json:"created_by,omitempty"`
	CreatedReason     string `json:"created_reason,omitempty"`
	SourceProfile     string `json:"source_profile,omitempty"`
	SourcePersonality string `json:"source_personality,omitempty"`
	ParentSessionID   string `json:"parent_session_id,omitempty"`
	ChildSessionID    string `json:"child_session_id,omitempty"`
	RunID             string `json:"run_id,omitempty"`
	StateMode         string `json:"state_mode,omitempty"`
	SourceTrigger     string `json:"source_trigger,omitempty"`
}

type addInput struct {
	Kind            string            `json:"kind"`
	Title           string            `json:"title,omitempty"`
	Text            string            `json:"text,omitempty"`
	Tags            []string          `json:"tags,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	Confidence      *float64          `json:"confidence,omitempty"`
	SourceSessionID string            `json:"source_session_id,omitempty"`
	SourceLinks     []sourceLinkInput `json:"source_links,omitempty"`
	Reason          string            `json:"reason,omitempty"`
}

type updateInput struct {
	ID          string   `json:"id"`
	Reason      string   `json:"reason,omitempty"`
	Replacement addInput `json:"replacement"`
}

type deleteInput struct {
	ID     string `json:"id"`
	Reason string `json:"reason,omitempty"`
}

type addOutput struct {
	Candidate memory.MemoryItem        `json:"candidate"`
	Memory    memory.MemoryItem        `json:"memory"`
	Decision  memory.PromotionDecision `json:"decision"`
}

type updateOutput struct {
	Previous    memory.MemoryItem        `json:"previous"`
	Replacement memory.MemoryItem        `json:"replacement"`
	Decision    memory.PromotionDecision `json:"decision"`
}

type deleteOutput struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Deleted bool   `json:"deleted"`
}

// AddDefinition returns the memory-write tool definition for adding memory.
func AddDefinition(runtime envtypes.Runtime) tools.Definition {
	return tools.Definition{
		Name:          "memory_add",
		Description:   "Create a source-linked semantic or procedural memory candidate and run it through promotion.",
		Groups:        []string{"core"},
		Requires:      tools.Capabilities{Memory: true},
		SemanticIndex: tools.SkipSemanticIndex(),
		Permission: permissions.Operation{
			Resource: permissions.ResourceMemory,
			Action:   permissions.ActionCreate,
			Effects:  []permissions.Effect{permissions.EffectWrite},
		},
		ResolvePermission: func(_ context.Context, call tools.Call) ([]permissions.EvaluationInput, error) {
			var input addInput
			if err := json.Unmarshal([]byte(call.Input), &input); err != nil {
				return nil, tools.NewPermissionResolutionError("invalid_input", "invalid tool input")
			}
			return []permissions.EvaluationInput{{Operation: permissions.Operation{
				Resource: permissions.ResourceMemory,
				Action:   permissions.ActionCreate,
				Effects:  []permissions.Effect{permissions.EffectWrite},
				Target:   str.String(input.Kind).Normalized(),
			}}}, nil
		},
		UsageInstruction: instructions.BuildMemoryAddGuidance(),
		InputSchema:      addInputSchema(),
		Handler: tools.HandlerFunc(func(ctx context.Context, call tools.Call) (tools.Result, error) {
			var input addInput
			if result := common.DecodeInput(call, &input); result.Error != "" {
				return result, nil
			}
			if runtime == nil {
				return common.ToolError("tool_error", "memory write is not configured"), nil
			}

			runCtx, hasRunContext := tools.RunContextFromContext(ctx)
			item, err := memoryItemFromAddInput(
				ctx,
				input,
				runCtx,
				hasRunContext,
				"tool_write",
				tools.TraceRecorderFromContext(ctx),
			)
			if err != nil {
				return common.ToolError("invalid_input", err.Error()), nil
			}

			var candidate memory.MemoryItem
			switch item.Kind {
			case memory.KindSemantic:
				candidate, err = runtime.RecordSemanticMemory(ctx, memory.SemanticRecord{Item: item})
			case memory.KindProcedural:
				candidate, err = runtime.RecordProceduralMemory(ctx, memory.ProceduralRecord{Item: item})
			}
			if err != nil {
				return common.ToolError("tool_error", err.Error()), nil
			}

			lifecycle, err := runtime.PromoteMemoryCandidate(ctx, memory.PromotionRequest{
				ID:     candidate.ID,
				Reason: getReason(input.Reason, "tool_memory_add"),
			})
			if err != nil {
				return common.ToolError("tool_error", err.Error()), nil
			}

			return common.EncodeOutput(addOutput{
				Candidate: candidate,
				Memory:    lifecycle.Item,
				Decision:  lifecycle.Decision,
			})
		}),
	}
}

// UpdateDefinition returns the memory-write tool definition for updating memory.
func UpdateDefinition(runtime envtypes.Runtime) tools.Definition {
	return tools.Definition{
		Name:          "memory_update",
		Description:   "Replace an active semantic or procedural memory with a source-linked candidate through lifecycle promotion.",
		Groups:        []string{"core"},
		Requires:      tools.Capabilities{Memory: true},
		SemanticIndex: tools.SkipSemanticIndex(),
		Permission: permissions.Operation{
			Resource: permissions.ResourceMemory,
			Action:   permissions.ActionUpdate,
			Effects:  []permissions.Effect{permissions.EffectWrite},
		},
		ResolvePermission: func(_ context.Context, call tools.Call) ([]permissions.EvaluationInput, error) {
			var input updateInput
			if err := json.Unmarshal([]byte(call.Input), &input); err != nil {
				return nil, tools.NewPermissionResolutionError("invalid_input", "invalid tool input")
			}
			return []permissions.EvaluationInput{{Operation: permissions.Operation{
				Resource: permissions.ResourceMemory,
				Action:   permissions.ActionUpdate,
				Effects:  []permissions.Effect{permissions.EffectWrite},
				Target:   str.String(input.ID).Trim(),
			}}}, nil
		},
		UsageInstruction: instructions.BuildMemoryUpdateGuidance(),
		InputSchema:      updateInputSchema(),
		Handler: tools.HandlerFunc(func(ctx context.Context, call tools.Call) (tools.Result, error) {
			var input updateInput
			if result := common.DecodeInput(call, &input); result.Error != "" {
				return result, nil
			}
			if runtime == nil {
				return common.ToolError("tool_error", "memory write is not configured"), nil
			}
			iDValue := str.String(input.ID)
			if iDValue.Trim() == "" {
				return common.ToolError("invalid_input", "memory id is required"), nil
			}

			runCtx, hasRunContext := tools.RunContextFromContext(ctx)
			replacement, err := memoryItemFromAddInput(
				ctx,
				input.Replacement,
				runCtx,
				hasRunContext,
				"tool_write",
				tools.TraceRecorderFromContext(ctx),
			)
			if err != nil {
				return common.ToolError("invalid_input", err.Error()), nil
			}

			result, err := runtime.UpdateMemory(ctx, memory.UpdateRequest{
				ID:          input.ID,
				Reason:      input.Reason,
				Replacement: replacement,
			})
			if err != nil {
				return common.ToolError("tool_error", err.Error()), nil
			}

			return common.EncodeOutput(updateOutput{
				Previous:    result.Previous,
				Replacement: result.Replacement,
				Decision:    result.Lifecycle.Decision,
			})
		}),
	}
}

// DeleteDefinition returns the memory-write tool definition for deleting memory.
func DeleteDefinition(runtime envtypes.Runtime) tools.Definition {
	return tools.Definition{
		Name:          "memory_delete",
		Description:   "Delete a durable memory through the memory lifecycle.",
		Groups:        []string{"core"},
		Requires:      tools.Capabilities{Memory: true},
		SemanticIndex: tools.SkipSemanticIndex(),
		Permission: permissions.Operation{
			Resource: permissions.ResourceMemory,
			Action:   permissions.ActionDelete,
			Effects:  []permissions.Effect{permissions.EffectWrite, permissions.EffectDestructive},
		},
		ResolvePermission: func(_ context.Context, call tools.Call) ([]permissions.EvaluationInput, error) {
			var input deleteInput
			if err := json.Unmarshal([]byte(call.Input), &input); err != nil {
				return nil, tools.NewPermissionResolutionError("invalid_input", "invalid tool input")
			}
			return []permissions.EvaluationInput{{Operation: permissions.Operation{
				Resource: permissions.ResourceMemory,
				Action:   permissions.ActionDelete,
				Effects:  []permissions.Effect{permissions.EffectWrite, permissions.EffectDestructive},
				Target:   str.String(input.ID).Trim(),
			}}}, nil
		},
		UsageInstruction: instructions.BuildMemoryDeleteGuidance(),
		InputSchema: common.ObjectSchema(map[string]any{
			"id":     common.StringSchema("Memory id to delete."),
			"reason": common.StringSchema("Concise user-grounded reason for deletion."),
		}, "id"),
		Handler: tools.HandlerFunc(func(ctx context.Context, call tools.Call) (tools.Result, error) {
			var input deleteInput
			if result := common.DecodeInput(call, &input); result.Error != "" {
				return result, nil
			}
			if runtime == nil {
				return common.ToolError("tool_error", "memory write is not configured"), nil
			}
			iDValue2 := str.String(input.ID)
			id := iDValue2.Trim()
			if id == "" {
				return common.ToolError("invalid_input", "memory id is required"), nil
			}

			if err := runtime.DeleteMemory(ctx, memory.DeleteRequest{ID: id, Reason: input.Reason}); err != nil {
				return common.ToolError("tool_error", err.Error()), nil
			}

			return common.EncodeOutput(deleteOutput{ID: id, Status: string(memory.StatusDeleted), Deleted: true})
		}),
	}
}

func memoryItemFromAddInput(
	ctx context.Context,
	input addInput,
	runCtx runcontext.Context,
	hasRunContext bool,
	trigger string,
	recorder tools.TraceRecorder,
) (memory.MemoryItem, error) {
	kind, err := parseKind(input.Kind)
	if err != nil {
		return memory.MemoryItem{}, err
	}

	confidence, err := getConfidence(input.Confidence)
	if err != nil {
		return memory.MemoryItem{}, err
	}
	titleValue := str.String(input.Title)
	textValue := str.String(input.Text)
	item := memory.MemoryItem{
		Kind:        kind,
		Title:       titleValue.Trim(),
		Text:        textValue.Trim(),
		Tags:        trimStrings(input.Tags),
		Metadata:    cloneMetadata(input.Metadata),
		SourceLinks: sourceLinksFromInput(input.SourceLinks),
		Confidence:  confidence,
	}
	if item.Metadata == nil {
		item.Metadata = make(map[string]string)
	}
	sourceSessionIDValue := str.String(input.SourceSessionID)
	if sessionID := sourceSessionIDValue.Trim(); sessionID != "" {
		item.Metadata[memory.MemoryMetadataSourceSessionID] = sessionID
	}
	if hasRunContext {
		item = memory.ApplyRunProvenance(item, runCtx, trigger)
	}
	if item.Title == "" && item.Text == "" {
		return memory.MemoryItem{}, errors.New("memory title or text is required")
	}
	if result, err := checkMemoryWriteSafety(item); err != nil {
		recordMemoryWriteSafetyBlocked(recorder, result)
		retainMemoryWriteSafetyEvidence(ctx, result)
		return memory.MemoryItem{}, err
	}
	if !hasProvenance(item) {
		return memory.MemoryItem{}, errors.New("memory source provenance is required")
	}

	return item, nil
}

type memoryWriteSafetyResult struct {
	Source        string
	ContentLength int
	Blocked       bool
	Redacted      bool
	Findings      []guardrails.SafetyFinding
	Original      memory.MemoryItem
	Safe          any
}

func checkMemoryWriteSafety(item memory.MemoryItem) (memoryWriteSafetyResult, error) {
	joinValue := str.String(strings.Join([]string{item.Title, item.Text}, "\n"))
	content := joinValue.Trim()
	result := memoryWriteSafetyResult{
		Source:        item.GuardrailSource(),
		ContentLength: len([]rune(content)),
		Original:      item,
	}
	if content == "" {
		return result, nil
	}

	inputSafety := guardrails.CheckInputSafety(content, item.GuardrailSource())
	if inputSafety.Blocked {
		result.Blocked = true
		result.Findings = inputSafety.Findings
		result.Safe = inputSafety.RefusalMessage
		return result, errors.New("memory content failed safety check")
	}

	outputSafety := guardrails.CheckOutputSafety(
		content,
		item.GuardrailSource(),
		guardrails.NewRedactorWithOptions(guardrails.RedactorOptions{DisablePII: true}),
	)
	if outputSafety.Blocked || outputSafety.Redacted {
		result.Blocked = outputSafety.Blocked
		result.Redacted = outputSafety.Redacted
		result.Findings = outputSafety.Findings
		result.Safe = outputSafety.Content
		return result, errors.New("memory content failed safety check")
	}

	return result, nil
}

func retainMemoryWriteSafetyEvidence(ctx context.Context, result memoryWriteSafetyResult) {
	action := "blocked"
	if result.Redacted && !result.Blocked {
		action = "redacted"
	}
	guardrails.RetainUnsafeEvidence(
		ctx,
		guardrails.UnsafeEvidenceRecorderFromContext(ctx),
		guardrails.UnsafeEvidence{
			Source:   result.Source,
			Action:   action,
			Blocked:  result.Blocked,
			Redacted: result.Redacted,
			Findings: guardrails.SafetyFindingLogFields(result.Findings),
			Original: result.Original,
			Safe:     result.Safe,
		},
	)
}

func recordMemoryWriteSafetyBlocked(recorder tools.TraceRecorder, result memoryWriteSafetyResult) {
	if recorder == nil {
		return
	}

	action := "blocked"
	if result.Redacted && !result.Blocked {
		action = "redacted"
	}
	recorder.Record(trace.EvtMemorySafetyBlocked, trace.SafetyEventPayload{
		Source:        result.Source,
		Action:        action,
		ContentLength: result.ContentLength,
		Blocked:       result.Blocked || result.Redacted,
		Redacted:      result.Redacted,
		Findings:      guardrails.SafetyFindingLogFields(result.Findings),
	})
}

func parseKind(value string) (memory.Kind, error) {
	valueText := str.String(value)
	switch valueText.Normalized() {
	case string(memory.KindSemantic):
		return memory.KindSemantic, nil
	case string(memory.KindProcedural):
		return memory.KindProcedural, nil
	default:
		return "", errors.New("memory kind must be semantic or procedural")
	}
}

func sourceLinksFromInput(inputs []sourceLinkInput) []memory.SourceLink {
	links := make([]memory.SourceLink, 0, len(inputs))
	for _, input := range inputs {
		sessionIDValue := str.String(input.SessionID)
		createdByValue := str.String(input.CreatedBy)
		createdReasonValue := str.String(input.CreatedReason)
		sourceProfileValue := str.String(input.SourceProfile)
		sourcePersonalityValue := str.String(input.SourcePersonality)
		parentSessionIDValue := str.String(input.ParentSessionID)
		childSessionIDValue := str.String(input.ChildSessionID)
		runIDValue := str.String(input.RunID)
		stateModeValue := str.String(input.StateMode)
		sourceTriggerValue := str.String(input.SourceTrigger)
		link := memory.SourceLink{
			SessionID:         sessionIDValue.Trim(),
			MessageIDs:        append([]uint(nil), input.MessageIDs...),
			Offsets:           append([]int(nil), input.Offsets...),
			CreatedBy:         createdByValue.Trim(),
			CreatedReason:     createdReasonValue.Trim(),
			SourceProfile:     sourceProfileValue.Trim(),
			SourcePersonality: sourcePersonalityValue.Trim(),
			ParentSessionID:   parentSessionIDValue.Trim(),
			ChildSessionID:    childSessionIDValue.Trim(),
			RunID:             runIDValue.Trim(),
			StateMode:         stateModeValue.Trim(),
			SourceTrigger:     sourceTriggerValue.Trim(),
		}
		if link.SessionID == "" &&
			len(link.MessageIDs) == 0 &&
			len(link.Offsets) == 0 {
			continue
		}
		links = append(links, link)
	}

	return links
}

func hasProvenance(item memory.MemoryItem) bool {
	return memory.HasSourceProvenance(item)
}

func cloneMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(metadata))
	for key, value := range metadata {
		keyValue := str.String(key)
		if key = keyValue.Trim(); key != "" {
			value2 := str.String(value)
			cloned[key] = value2.Trim()
		}
	}

	return cloned
}

func trimStrings(values []string) []string {
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		value3 := str.String(value)
		value = value3.Trim()
		if value != "" {
			trimmed = append(trimmed, value)
		}
	}

	return trimmed
}

func getReason(value string, fallback string) string {
	value4 := str.String(value)
	if value = value4.Trim(); value != "" {
		return value
	}

	return fallback
}

func getConfidence(value *float64) (float64, error) {
	if value == nil {
		return 1, nil
	}
	if *value < 0 || *value > 1 {
		return 0, errors.New("memory confidence must be between 0 and 1")
	}

	return *value, nil
}

func addInputSchema() map[string]any {
	return common.ObjectSchema(map[string]any{
		"kind":              enumSchema("Memory kind to create.", "semantic", "procedural"),
		"title":             common.StringSchema("Short memory title."),
		"text":              common.StringSchema("Durable memory text."),
		"tags":              stringArraySchema("Optional concise tags."),
		"metadata":          stringMapSchema("Optional string metadata used by admission and provenance."),
		"confidence":        numberSchema("Confidence from 0 to 1. Defaults to 1 for explicit user-directed writes."),
		"source_session_id": common.StringSchema("Source session id when source_links are unavailable."),
		"source_links":      sourceLinksSchema(),
		"reason":            common.StringSchema("Concise user-grounded reason for the write."),
	}, "kind")
}

func updateInputSchema() map[string]any {
	return common.ObjectSchema(map[string]any{
		"id":          common.StringSchema("Existing active memory id to replace."),
		"reason":      common.StringSchema("Concise user-grounded reason for the update."),
		"replacement": addInputSchema(),
	}, "id", "replacement")
}

func sourceLinksSchema() map[string]any {
	return map[string]any{
		"type":        "array",
		"description": "Optional source links proving where the write came from.",
		"items": common.ObjectSchema(map[string]any{
			"session_id":         common.StringSchema("Source session id."),
			"message_ids":        integerArraySchema("Source message ids."),
			"offsets":            integerArraySchema("Source message offsets."),
			"created_by":         common.StringSchema("Provenance creator."),
			"created_reason":     common.StringSchema("Provenance reason."),
			"source_profile":     common.StringSchema("Source profile name."),
			"source_personality": common.StringSchema("Source personality name."),
			"parent_session_id":  common.StringSchema("Parent session id for child writes."),
			"child_session_id":   common.StringSchema("Child session id."),
			"run_id":             common.StringSchema("Child run id."),
			"state_mode":         common.StringSchema("State mode for the source run."),
			"source_trigger":     common.StringSchema("Trigger that produced this source link."),
		}),
	}
}

func enumSchema(description string, values ...string) map[string]any {
	return map[string]any{
		"type":        "string",
		"description": description,
		"enum":        values,
	}
}

func stringArraySchema(description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items":       map[string]any{"type": "string"},
	}
}

func integerArraySchema(description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items":       map[string]any{"type": "integer"},
	}
}

func numberSchema(description string) map[string]any {
	return map[string]any{
		"type":        "number",
		"description": description,
	}
}

func stringMapSchema(description string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"description":          description,
		"additionalProperties": map[string]any{"type": "string"},
	}
}
