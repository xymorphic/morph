package providercmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	cli "github.com/urfave/cli/v3"
)

func TestCommand_ExposesProviderOperations(t *testing.T) {
	command := NewCommand(strings.NewReader(""), &bytes.Buffer{})

	require.Equal(t, "provider", command.Name)
	require.Equal(t, []string{"login", "status", "logout", "configure"}, commandNames(command))
}

func TestCommand_ShowsHelpWithoutOperation(t *testing.T) {
	var output bytes.Buffer
	command := NewCommand(strings.NewReader(""), &output)
	command.Writer = &output

	err := command.Run(context.Background(), []string{"provider"})

	require.NoError(t, err)
	require.Contains(t, output.String(), "login")
	require.Contains(t, output.String(), "configure")
}

func commandNames(command *cli.Command) []string {
	names := make([]string, 0, len(command.Commands))
	for _, child := range command.Commands {
		names = append(names, child.Name)
	}
	return names
}
