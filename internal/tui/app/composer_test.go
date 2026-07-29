package tui

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseComposerInput_ClassifiesInput(t *testing.T) {
	cases := []struct {
		name string
		text string
		want composerInput
	}{
		{
			name: "empty",
			text: " \n\t ",
			want: composerInput{Kind: composerInputEmpty},
		},
		{
			name: "prompt",
			text: " hello ",
			want: composerInput{Kind: composerInputPrompt, Text: "hello"},
		},
		{
			name: "command",
			text: " /use project-a ",
			want: composerInput{
				Kind: composerInputCommand,
				Text: "/use project-a",
				Name: "use",
				Args: "project-a",
			},
		},
		{
			name: "effort command preserves argument case",
			text: " /EFFORT High ",
			want: composerInput{
				Kind: composerInputCommand,
				Text: "/EFFORT High",
				Name: "effort",
				Args: "High",
			},
		},
		{
			name: "local command",
			text: " !git status ",
			want: composerInput{
				Kind: composerInputLocalCommand,
				Text: "!git status",
				Args: "git status",
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, parseComposerInput(tt.text))
		})
	}
}

func TestNormalizeComposerPaste_TrimsTrailingLineBreaks(t *testing.T) {
	require.Equal(t, "first\n\nsecond", normalizeComposerPaste("first\n\nsecond\n\r\n"))
	require.Equal(t, "first\n\nsecond", normalizeComposerPaste("first\n\nsecond"))
}
