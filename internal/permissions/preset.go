package permissions

import (
	"errors"
	"strings"

	"github.com/xymorphic/morph/pkg/str"
)

type Preset string

const (
	PresetAskForApproval Preset = "ask"
	PresetApproveForMe   Preset = "approve"
	PresetFullAccess     Preset = "full_access"
	PresetCustom         Preset = "custom"
)

func ParsePreset(value string) (Preset, error) {
	value = strings.ReplaceAll(str.String(value).Normalized(), "-", "_")
	preset := Preset(value)
	if !isValidPreset(preset) {
		return "", errors.New("permission preset must be one of: ask, approve, full_access, custom")
	}

	return preset, nil
}

func (p Preset) Label() string {
	switch p {
	case PresetAskForApproval:
		return "Ask for approval"
	case PresetApproveForMe:
		return "Approve for me"
	case PresetFullAccess:
		return "Full access"
	case PresetCustom:
		return "Custom"
	default:
		return ""
	}
}

func (p Preset) Description() string {
	switch p {
	case PresetAskForApproval:
		return "Ask before commands, external edits, and internet access"
	case PresetApproveForMe:
		return "Ask only for potentially unsafe actions"
	case PresetFullAccess:
		return "Unrestricted files, commands, and internet"
	case PresetCustom:
		return "Use the detailed profile permission policy"
	default:
		return ""
	}
}

func (p Policy) Label() string {
	p.Normalize()
	label := p.Preset.Label()
	if len(p.Rules) > 0 && (p.Preset == PresetAskForApproval || p.Preset == PresetApproveForMe) {
		return label + " (customized)"
	}

	return label
}

func isValidPreset(preset Preset) bool {
	return preset == PresetAskForApproval ||
		preset == PresetApproveForMe ||
		preset == PresetFullAccess ||
		preset == PresetCustom
}

func (p Policy) EffectivePreset() Preset {
	p.Normalize()
	return p.Preset
}

func (p Policy) Effective() Policy {
	p.Normalize()
	return p.ForPreset(p.Preset)
}

func (p Policy) ForPreset(preset Preset) Policy {
	p.Normalize()
	if !isValidPreset(preset) {
		preset = PresetCustom
	}
	if preset == PresetCustom {
		p.Preset = PresetCustom
		p.presetRules = nil
		return p
	}

	effective := p
	effective.Preset = preset
	effective.presetRules = nil
	effective.Default = DecisionDeny
	effective.SurfaceDefaults = nil
	effective.SurfaceKindDefaults = map[SurfaceKind]Decision{
		SurfaceKindLocal:      DecisionDeny,
		SurfaceKindGateway:    DecisionDeny,
		SurfaceKindAutomation: DecisionDeny,
		SurfaceKindRPC:        DecisionDeny,
		SurfaceKindACP:        DecisionDeny,
	}

	switch preset {
	case PresetAskForApproval:
		effective.presetRules = askForApprovalPresetRules()
	case PresetApproveForMe:
		effective.presetRules = approveForMePresetRules()
	case PresetFullAccess:
		effective.presetRules = nil
	}

	return effective
}

func askForApprovalPresetRules() []Rule {
	rules := approveForMePresetRules()
	rules = append(rules,
		Rule{
			Name:         "preset.ask.execution",
			SurfaceKinds: []SurfaceKind{SurfaceKindLocal},
			Effects:      []Effect{EffectExecution},
			Decision:     DecisionAsk,
			Reason:       "This action runs a program on your computer.",
			toolRequired: true,
		},
		Rule{
			Name:         "preset.ask.browser_network",
			SurfaceKinds: []SurfaceKind{SurfaceKindLocal},
			Tools:        []string{"browser"},
			Effects:      []Effect{EffectNetwork},
			Decision:     DecisionAsk,
			Reason:       "This browser action may load content from or send data to a website.",
			toolRequired: true,
		},
		Rule{
			Name:         "preset.ask.network",
			SurfaceKinds: []SurfaceKind{SurfaceKindLocal},
			Effects:      []Effect{EffectNetwork},
			Decision:     DecisionAsk,
			Reason:       "This action connects to an online service to send or receive data.",
			toolRequired: true,
		},
	)
	return rules
}

func approveForMePresetRules() []Rule {
	return []Rule{
		{
			Name:         "preset.local_owner",
			ActorKinds:   []ActorKind{ActorLocalOwner},
			SurfaceKinds: []SurfaceKind{SurfaceKindLocal},
			Decision:     DecisionAllow,
			Reason:       "interactive local owner",
		},
		{
			Name:         "preset.safety.destructive",
			SurfaceKinds: []SurfaceKind{SurfaceKindLocal},
			Effects:      []Effect{EffectDestructive},
			Decision:     DecisionAsk,
			Reason:       "This action can permanently delete or overwrite data.",
			toolRequired: true,
		},
		{
			Name:         "preset.safety.credentials",
			SurfaceKinds: []SurfaceKind{SurfaceKindLocal},
			Effects:      []Effect{EffectCredentialBearing},
			Decision:     DecisionAsk,
			Reason:       "This action can access or send credentials or signed-in account data.",
			toolRequired: true,
		},
		{
			Name:         "preset.safety.privileges",
			SurfaceKinds: []SurfaceKind{SurfaceKindLocal},
			Effects:      []Effect{EffectPrivilegeChanging},
			Decision:     DecisionAsk,
			Reason:       "This action can change permissions or security settings.",
			toolRequired: true,
		},
		{
			Name:         "preset.safety.external_write",
			SurfaceKinds: []SurfaceKind{SurfaceKindLocal},
			Effects:      []Effect{EffectWrite},
			TargetScopes: []TargetScope{TargetScopeExternal},
			Decision:     DecisionAsk,
			Reason:       "This action changes files outside Morph's workspace.",
			toolRequired: true,
		},
		{
			Name:         "preset.safety.indirect_execution",
			SurfaceKinds: []SurfaceKind{SurfaceKindLocal},
			Effects:      []Effect{EffectIndirectExecution},
			Decision:     DecisionAsk,
			Reason:       "This command can discover and run additional instructions at runtime.",
			toolRequired: true,
		},
		{
			Name:         "preset.safety.shared_state",
			SurfaceKinds: []SurfaceKind{SurfaceKindLocal},
			Effects:      []Effect{EffectSharedState},
			Decision:     DecisionAsk,
			Reason:       "This action can observe or modify execution state shared with other sessions.",
			toolRequired: true,
		},
	}
}
