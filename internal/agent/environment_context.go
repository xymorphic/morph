package agent

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"time"

	"github.com/xymorphic/morph/internal/guardrails"
	instruct "github.com/xymorphic/morph/internal/instructions"
	models "github.com/xymorphic/morph/internal/model"
	"github.com/xymorphic/morph/internal/permissions"
	storage "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/internal/tools"
	agentsession "github.com/xymorphic/morph/pkg/agent/session"
	agenttool "github.com/xymorphic/morph/pkg/agent/tool"
	"github.com/xymorphic/morph/pkg/str"
)

var (
	environmentContextNow   = time.Now
	environmentContextGetwd = os.Getwd
)

type toolGroupLister interface {
	ListGroups() []agenttool.Group
}

// buildEnvironmentContextInstruction renders runtime facts the model needs for this turn.
func (t *Turn) buildEnvironmentContextInstruction(activeToolDefinitions []models.ToolDefinition) instruct.Instruction {
	if t == nil {
		return instruct.Instruction{Name: instruct.EnvironmentContextInstructionName}
	}

	now := environmentContextNow()
	workingDirectory, _ := environmentContextGetwd()

	ctx := instruct.EnvironmentContext{
		Now:              now,
		Timezone:         getEnvironmentTimezone(now),
		OS:               runtime.GOOS,
		Architecture:     runtime.GOARCH,
		WorkingDirectory: workingDirectory,
		SessionID:        t.sessionID,
	}

	// Config provides static runtime facts such as provider, model, API,
	// and allowed filesystem roots.
	if t.cfg != nil {
		ctx.Platform = t.cfg.Platform
		ctx.FilesystemRoots = getFilesystemRoots(t.cfg.FS.Roots, workingDirectory)
		permissionPolicy := t.cfg.Permissions
		permissionPolicy.Normalize()
		if preset, ok := permissions.PresetFromContext(t.ctx); ok {
			permissionPolicy = permissionPolicy.ForPreset(preset)
		} else {
			permissionPolicy = permissionPolicy.Effective()
		}
		ctx.FullAccess = permissionPolicy.EffectivePreset() == permissions.PresetFullAccess
		ctx.Model = t.cfg.Models.Main.Name
		if summaryModel := t.cfg.SummaryModelEffective(); summaryModel != "" && summaryModel != t.cfg.Models.Main.Name {
			ctx.SummaryModel = summaryModel
		}
		ctx.ModelProvider = t.cfg.Models.Main.Provider
		if summaryProvider := t.cfg.SummaryProviderEffective(); summaryProvider != "" &&
			summaryProvider != t.cfg.Models.Main.Provider {
			ctx.SummaryProvider = summaryProvider
		}
		ctx.API = t.cfg.MainModelAPIEffective()
		ctx.WebProvider = t.cfg.Web.Provider
	}

	// The live tool policy can override platform and capability details because
	// it may be narrower than the static config.
	if policy, ok := t.getActiveToolPolicy(); ok {
		ctx.Platform = getFirstNonEmpty(ctx.Platform, policy.Platform)
		ctx.Capabilities = instruct.EnvironmentCapabilities{
			Filesystem: policy.Capabilities.Filesystem,
			Network:    policy.Capabilities.Network,
			Exec:       policy.Capabilities.Exec,
			Memory:     policy.Capabilities.Memory,
			Browser:    policy.Capabilities.Browser,
		}
		ctx.HasCapabilities = true

		if groups := t.getActiveToolGroups(); len(activeToolDefinitions) > 0 && len(groups) > 0 {
			ctx.ActiveToolGroups = getActiveToolGroups(groups)
		}
	}

	ctx.ActiveTools = getActiveToolNames(activeToolDefinitions)
	ctx.SessionOrigin = environmentSessionOriginFromStorage(t.sessionOrigin)

	return instruct.BuildEnvironmentContext(ctx)
}

func environmentSessionOriginFromStorage(origin storage.SessionOrigin) instruct.EnvironmentSessionOrigin {
	return instruct.EnvironmentSessionOrigin{
		AccountID:      origin.AccountID,
		ConversationID: origin.ConversationID,
		Source:         origin.Source,
		ThreadID:       origin.ThreadID,
	}
}

func storageSessionOriginFromAgentSessionOrigin(origin agentsession.Origin) storage.SessionOrigin {
	return storage.SessionOrigin{
		AccountID:      origin.AccountID,
		ConversationID: origin.ConversationID,
		Source:         origin.Source,
		ThreadID:       origin.ThreadID,
	}
}

func (t *Turn) getActiveToolPolicy() (agenttool.Policy, bool) {
	if t == nil {
		return agenttool.Policy{}, false
	}
	if policy, ok := t.environmentToolPolicy(); ok {
		return agentToolPolicyFromToolsPolicy(policy), true
	}
	if t.toolRegistry != nil {
		return t.toolPolicy, true
	}

	return agenttool.Policy{}, false
}

func (t *Turn) getActiveToolGroups() []agenttool.Group {
	if t == nil {
		return nil
	}
	if registry, ok := t.environmentToolRegistry(); ok {
		return agentToolGroupsFromToolsGroups(registry.ListGroups())
	}
	if groupLister, ok := t.toolRegistry.(toolGroupLister); ok {
		return groupLister.ListGroups()
	}

	return nil
}

func agentToolPolicyFromToolsPolicy(policy tools.Policy) agenttool.Policy {
	return agenttool.Policy{
		GroupNames: append([]string(nil), policy.GroupNames...),
		Capabilities: agenttool.Capabilities{
			Filesystem: policy.Capabilities.Filesystem,
			Network:    policy.Capabilities.Network,
			Exec:       policy.Capabilities.Exec,
			Browser:    policy.Capabilities.Browser,
			Memory:     policy.Capabilities.Memory,
		},
		Platform: policy.Platform,
	}
}

func agentToolGroupsFromToolsGroups(groups []tools.Group) []agenttool.Group {
	if len(groups) == 0 {
		return nil
	}

	result := make([]agenttool.Group, 0, len(groups))
	for _, group := range groups {
		result = append(result, agenttool.Group{
			Name:     group.Name,
			Tools:    append([]string(nil), group.Tools...),
			Includes: append([]string(nil), group.Includes...),
		})
	}

	return result
}

// getEnvironmentTimezone returns a human-readable timezone description.
func getEnvironmentTimezone(now time.Time) string {
	if now.IsZero() || now.Location() == nil {
		return ""
	}

	location := now.Location().String()
	if location != "" && location != "Local" {
		return location
	}

	name, offset := now.Zone()
	if name == "" {
		return location
	}
	trimmedValue := str.String(location + " (" + name + ", UTC" + getTimezoneOffset(offset) + ")")
	return trimmedValue.
		Trim()

}

// getTimezoneOffset formats a seconds-east-of-UTC offset as +/-HH:MM.
func getTimezoneOffset(offset int) string {
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	return sign + fmt.Sprintf("%02d:%02d", offset/3600, (offset%3600)/60)
}

// getFilesystemRoots normalizes configured roots and falls back to the process working directory.
func getFilesystemRoots(configured []string, workingDirectory string) []string {
	roots := configured
	workingDirectoryValue := str.String(workingDirectory)
	if len(roots) == 0 && workingDirectoryValue.Trim() != "" {
		roots = []string{workingDirectory}
	}
	return guardrails.NormalizeRoots(roots)
}

// getActiveToolNames returns sorted unique tool names visible to the model.
func getActiveToolNames(definitions []models.ToolDefinition) []string {
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Name)
	}

	return sortedUnique(names)
}

// getActiveToolGroups returns sorted unique tool group names for active tools.
func getActiveToolGroups(groups []agenttool.Group) []string {
	names := make([]string, 0, len(groups))
	for _, group := range groups {
		names = append(names, group.Name)
	}

	return sortedUnique(names)
}

// sortedUnique trims, deduplicates, and sorts a string list for stable prompts.
func sortedUnique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		valueText := str.String(value).Trim()
		if valueText == "" {
			continue
		}
		if _, ok := seen[valueText]; ok {
			continue
		}
		seen[valueText] = struct{}{}
		cleaned = append(cleaned, valueText)
	}
	sort.Strings(cleaned)
	return cleaned
}

// getFirstNonEmpty returns first when set, otherwise a trimmed second value.
func getFirstNonEmpty(first, second string) string {
	firstValue := str.String(first)
	if firstValue.Trim() != "" {
		return first
	}
	secondValue := str.String(second)
	return secondValue.Trim()
}
