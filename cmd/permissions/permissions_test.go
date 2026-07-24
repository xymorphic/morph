package permissions

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewCommand_ExposesApprovalLifecycleCommands(t *testing.T) {
	command := NewCommand()
	names := make([]string, len(command.Commands))
	for index, child := range command.Commands {
		names[index] = child.Name
	}
	require.ElementsMatch(t, []string{
		"list", "pending", "grants", "inspect", "preset", "prune", "approve", "deny", "revoke", "delete", "explain",
	}, names)
	require.NoError(t, command.Run(context.Background(), []string{"permissions"}))
}
