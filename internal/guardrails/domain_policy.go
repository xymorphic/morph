package guardrails

import (
	"bufio"
	"os"
	"strings"

	"github.com/xymorphic/morph/pkg/str"
)

type domainRule struct {
	Pattern string
	Source  string
}

func appendDomainRules(existing []domainRule, values []string, source string) []domainRule {
	for _, value := range values {
		rule := normalizeWebsiteRule(value)
		if rule == "" {
			continue
		}
		sourceValue := str.String(source)
		existing = append(existing, domainRule{
			Pattern: rule,
			Source:  sourceValue.Trim(),
		})
	}

	return existing
}

func appendDomainRulesFromFiles(existing []domainRule, files []string) []domainRule {
	for _, file := range files {
		fileValue := str.String(file)
		existing = appendDomainRules(existing, loadPolicyFile(file), fileValue.Trim())
	}

	return existing
}

func getFirstMatchingDomainRule(rules []domainRule, host string) (domainRule, bool) {
	host = normalizeWebsiteHost(host)
	if host == "" {
		return domainRule{}, false
	}

	for _, rule := range rules {
		if checkWebsiteRuleMatches(rule.Pattern, host) {
			return rule, true
		}
	}

	return domainRule{}, false
}

func loadPolicyFile(path string) []string {
	pathValue := str.String(path)
	path = pathValue.Trim()
	if path == "" {
		return nil
	}

	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()

	var values []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		textValue := str.String(scanner.Text())
		line := textValue.Trim()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		values = append(values, line)
	}

	return values
}
