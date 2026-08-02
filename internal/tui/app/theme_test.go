package tui

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xymorphic/morph/internal/tui/render"
	"github.com/xymorphic/morph/pkg/termtheme"
)

func TestAdaptTUITheme_UsesDerivedDarkPaletteForNonBlackBackground(t *testing.T) {
	theme := adaptTUITheme(render.DefaultTheme, termtheme.Result{
		Theme:      "dark",
		Background: "#1e1e2e",
		Source:     "osc11",
	})

	require.Equal(t, "#2a2a39", theme.UserTranscriptBackground)
	require.Equal(t, "#5d5d68", theme.InputFrameBorder)
	require.Equal(t, "#3d3d4b", theme.MarkdownCodeBackground)
	require.Equal(t, "#61616c", theme.NoticeBorder)
	require.Equal(t, "#4f4f5b", theme.CompactionText)
	require.Equal(t, "#363644", theme.CompactionSeparator)
	require.NotEqual(t, render.DefaultTheme.UserTranscriptBackground, theme.UserTranscriptBackground)
}

func TestAdaptTUITheme_OnlyAdaptsCompactionPaletteForBlackBackground(t *testing.T) {
	theme := adaptTUITheme(render.DefaultTheme, termtheme.Result{
		Theme:      "dark",
		Background: "#000000",
		Source:     "osc11",
	})

	require.Equal(t, render.DefaultTheme.UserTranscriptBackground, theme.UserTranscriptBackground)
	require.Equal(t, "#383838", theme.CompactionText)
	require.Equal(t, "#1c1c1c", theme.CompactionSeparator)
}

func TestAdaptTUITheme_RaisesCompactionContrastForLightBackground(t *testing.T) {
	theme := adaptTUITheme(render.DefaultTheme, termtheme.Result{
		Theme:      "light",
		Background: "#f5f5f5",
		Source:     "osc11",
	})

	require.Equal(t, render.DefaultTheme.UserTranscriptBackground, theme.UserTranscriptBackground)
	require.Equal(t, "#8e8e8e", theme.CompactionText)
	require.Equal(t, "#c8c8c8", theme.CompactionSeparator)
}

func TestAdaptTUITheme_KeepsDefaultPaletteForUnknownBackground(t *testing.T) {
	unknown := adaptTUITheme(render.DefaultTheme, termtheme.Result{
		Theme:  "unknown",
		Source: "tty",
		Error:  "missing tty",
	})

	require.Equal(t, render.DefaultTheme, unknown)
}
