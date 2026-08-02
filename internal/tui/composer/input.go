package composer

import (
	"strings"

	"github.com/xymorphic/morph/pkg/str"
)

// InputKind classifies composer input before submission.
type InputKind int

const (
	InputEmpty InputKind = iota
	InputPrompt
	InputCommand
	InputLocalCommand
)

// Input describes input for input.
type Input struct {
	Kind InputKind
	Text string
	Name string
	Args string
}

// ParseInput classifies raw composer input as a command or chat message.
func ParseInput(value string) Input {
	valueText := str.String(value)
	text := valueText.Trim()
	if text == "" {
		return Input{Kind: InputEmpty}
	}

	if command, ok := strings.CutPrefix(text, "/"); ok {
		commandValue := str.String(command)
		name, args, _ := strings.Cut(commandValue.Trim(), " ")
		nameValue := str.String(name)
		argsValue := str.String(args)
		return Input{
			Kind: InputCommand,
			Text: text,
			Name: nameValue.Normalized(),
			Args: argsValue.Trim(),
		}
	}

	if command, ok := strings.CutPrefix(text, "!"); ok {
		commandValue2 := str.String(command)
		return Input{
			Kind: InputLocalCommand,
			Text: text,
			Args: commandValue2.Trim(),
		}
	}

	return Input{Kind: InputPrompt, Text: text}
}

// NormalizePaste normalizes paste.
func NormalizePaste(value string) string {
	return strings.TrimRight(value, "\r\n")
}
