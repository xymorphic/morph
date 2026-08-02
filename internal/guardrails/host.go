package guardrails

import "github.com/xymorphic/morph/pkg/str"

// HostPolicy defines host policy settings.
type HostPolicy struct {
	AllowRules []HostRule
	DenyRules  []HostRule
}

// HostRule classifies host safety rules.
type HostRule = domainRule

// HostBlock describes one blocked host rule.
type HostBlock struct {
	Host    string
	Rule    string
	Source  string
	Message string
}

// NewHostPolicy returns a host safety policy compiled from blocked host rules.
func NewHostPolicy(allowHosts, denyHosts, allowFiles, denyFiles []string) HostPolicy {
	allowRules := appendDomainRules(nil, allowHosts, "config")
	allowRules = appendDomainRulesFromFiles(allowRules, allowFiles)
	denyRules := appendDomainRules(nil, denyHosts, "config")
	denyRules = appendDomainRulesFromFiles(denyRules, denyFiles)

	return HostPolicy{
		AllowRules: allowRules,
		DenyRules:  denyRules,
	}
}

func (p HostPolicy) Check(host string) (HostBlock, bool) {
	host = normalizeWebsiteHost(host)
	if host == "" {
		return HostBlock{}, false
	}

	if rule, ok := getFirstMatchingDomainRule(p.DenyRules, host); ok {
		message := `blocked by configured native host denylist policy: "` + host + `" matched "` + rule.Pattern + `"`
		sourceValue := str.String(rule.Source)
		if source := sourceValue.Trim(); source != "" {
			message += ` from "` + source + `"`
		}

		return HostBlock{
			Host:    host,
			Rule:    rule.Pattern,
			Source:  rule.Source,
			Message: message,
		}, true
	}

	if len(p.AllowRules) == 0 {
		return HostBlock{}, false
	}

	if _, ok := getFirstMatchingDomainRule(p.AllowRules, host); ok {
		return HostBlock{}, false
	}

	return HostBlock{
		Host:    host,
		Message: `blocked by configured native host allowlist policy: "` + host + `" did not match any allowed host rule`,
	}, true
}
