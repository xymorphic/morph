package guardrails

import (
	"net/url"
	"strings"

	"github.com/xymorphic/morph/pkg/str"
)

// WebsitePolicy defines website policy settings.
type WebsitePolicy struct {
	Enabled bool
	Rules   []WebsiteRule
}

// WebsiteRule classifies website safety rules.
type WebsiteRule = domainRule

// WebsiteBlock describes one blocked website rule.
type WebsiteBlock struct {
	URL     string
	Host    string
	Rule    string
	Source  string
	Message string
}

// NewWebsitePolicy returns a website safety policy compiled from blocked website rules.
func NewWebsitePolicy(enabled bool, domains, files []string) WebsitePolicy {
	rules := appendDomainRules(nil, domains, "config")
	rules = appendDomainRulesFromFiles(rules, files)
	return WebsitePolicy{Enabled: enabled, Rules: rules}
}

func (p WebsitePolicy) Check(rawURL string) (WebsiteBlock, bool) {
	if !p.Enabled || len(p.Rules) == 0 {
		return WebsiteBlock{}, false
	}

	host := getHostFromWebsiteURL(rawURL)
	if host == "" {
		return WebsiteBlock{}, false
	}

	if rule, ok := getFirstMatchingDomainRule(p.Rules, host); ok {
		message := getWebsiteBlockMessage(host, rule)
		rawURLValue := str.String(rawURL)
		return WebsiteBlock{
			URL:     rawURLValue.Trim(),
			Host:    host,
			Rule:    rule.Pattern,
			Source:  rule.Source,
			Message: message,
		}, true
	}

	return WebsiteBlock{}, false
}

func getWebsiteBlockMessage(host string, rule WebsiteRule) string {
	message := `blocked by configured website blocklist policy: "` + host + `" matched "` + rule.Pattern + `"`
	sourceValue := str.String(rule.Source)
	source := sourceValue.Trim()
	if source == "" {
		return message
	}

	return message + ` from "` + source + `"`
}

func normalizeWebsiteRule(value string) string {
	valueText := str.String(value).Normalized()
	if valueText == "" {
		return ""
	}

	wildcard := strings.HasPrefix(valueText, "*.")
	if wildcard {
		valueText = strings.TrimPrefix(valueText, "*.")
	}

	host := getHostFromWebsiteURL(valueText)
	if host == "" {
		if strings.Contains(valueText, "://") {
			return ""
		}

		host = normalizeWebsiteHost(valueText)
	}
	if host == "" {
		return ""
	}
	if wildcard {
		return "*." + host
	}

	return host
}

func getHostFromWebsiteURL(rawURL string) string {
	rawURLValue2 := str.String(rawURL)
	rawURL = rawURLValue2.Trim()
	if rawURL == "" {
		return ""
	}

	parsed, err := url.Parse(rawURL)
	if err == nil && parsed.Hostname() != "" {
		return normalizeWebsiteHost(parsed.Hostname())
	}
	if err == nil && parsed.Scheme != "" {
		return ""
	}

	parsed, err = url.Parse("//" + rawURL)
	if err != nil {
		return ""
	}

	return normalizeWebsiteHost(parsed.Hostname())
}

func normalizeWebsiteHost(host string) string {
	hostValue := str.String(host)
	host = hostValue.Normalized()
	host = strings.TrimSuffix(host, ".")

	return host
}

func checkWebsiteRuleMatches(rule, host string) bool {
	rule = normalizeWebsiteRule(rule)
	host = normalizeWebsiteHost(host)
	if rule == "" || host == "" {
		return false
	}

	if after, ok := strings.CutPrefix(rule, "*."); ok {
		suffix := after
		return host != suffix && strings.HasSuffix(host, "."+suffix)
	}

	return host == rule || strings.HasSuffix(host, "."+rule)
}
