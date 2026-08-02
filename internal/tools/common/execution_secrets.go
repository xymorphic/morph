package common

import (
	"slices"
	"strings"

	envtypes "github.com/wandxy/morph/internal/environment/types"
	"github.com/wandxy/morph/internal/execution"
)

func AddExecutionSecretsSchema(
	properties map[string]any,
	runtime envtypes.Runtime,
	description string,
) map[string]any {
	if runtime == nil {
		return properties
	}
	catalog := runtime.GetExecutionSecretCatalog()
	if len(catalog) == 0 {
		return properties
	}
	slices.SortFunc(catalog, func(left, right execution.SecretCatalogEntry) int {
		return strings.Compare(left.Name, right.Name)
	})
	names := make([]string, 0, len(catalog))
	details := make([]string, 0, len(catalog))
	for _, entry := range catalog {
		names = append(names, entry.Name)
		details = append(details, entry.Name+" — "+entry.Description)
	}
	properties["secrets"] = map[string]any{
		"type": "array",
		"description": JoinStrings(
			description,
			"Available references: "+strings.Join(details, "; ")+".",
		),
		"items": map[string]any{
			"type": "string",
			"enum": names,
		},
	}
	return properties
}
