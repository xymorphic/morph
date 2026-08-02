package pinned

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xymorphic/morph/internal/constants"
	"github.com/xymorphic/morph/internal/datadir"
	state "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/pkg/str"
)

const (
	defaultMaxChars     = constants.DefaultMemoryPinnedMaxChars
	defaultMaxItemChars = constants.DefaultMemoryPinnedItemChars
	defaultFileName     = constants.MemoryPinnedFileName
)

var (
	stat    = os.Stat
	readDir = os.ReadDir
)

// Options configures this package operation.
type Options struct {
	Enabled      *bool
	MaxChars     int
	MaxItemChars int
}

// SafetyScanner is injected by the provider so this package can prepare pinned
// items without importing provider guardrail implementations.
type SafetyScanner func(context.Context, state.MemoryItem) error

// Redactor is injected by the provider for the same reason as SafetyScanner:
// pinned memory preparation should stay reusable and storage-agnostic.
type Redactor func(context.Context, state.MemoryItem) (state.MemoryItem, error)

// AutoFile looks for the conventional profile-local pinned-memory file. Absence
// is not an error because most profiles will not have pinned memory configured.
func AutoFile() (string, bool, error) {
	return getAutoFileFromRoot(datadir.HomeDir())
}

// NormalizeOptions fills prompt-budget defaults. These budgets are enforced
// after safety scan and redaction because redaction can change rendered length.
func NormalizeOptions(opts Options) Options {
	if opts.MaxChars <= 0 {
		opts.MaxChars = defaultMaxChars
	}
	if opts.MaxItemChars <= 0 {
		opts.MaxItemChars = defaultMaxItemChars
	}
	return opts
}

// Enabled treats a nil option as enabled. That keeps pinned memory available by
// default while allowing config to explicitly disable it.
func Enabled(opts Options) bool {
	return opts.Enabled == nil || *opts.Enabled
}

// LoadFile converts a profile-local pinned-memory file into one active memory item.
// Store-backed pinned memories are loaded by the provider; this function only
// handles the operator-controlled file source.
func LoadFile() ([]state.MemoryItem, error) {
	file, ok, err := AutoFile()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("failed to read pinned memory file %q: %w", file, err)
	}
	dataValue := str.String(string(data))
	text := dataValue.Trim()
	if text == "" {
		return nil, nil
	}
	baseValue := str.String(filepath.Base(file))
	return []state.MemoryItem{{
		ID:     getFileMemoryID(file),
		Kind:   state.MemoryKindPinned,
		Status: state.MemoryStatusActive,
		Title:  baseValue.Trim(),
		Text:   text,
		Metadata: map[string]string{
			"source": "file",
			"path":   file,
		},
	}}, nil
}

// PrepareItems applies the prompt-facing preparation pipeline for pinned memory:
// active-only filtering, safety scan, redaction, per-item truncation, and total
// budget enforcement.
func PrepareItems(
	ctx context.Context,
	items []state.MemoryItem,
	query state.MemorySearchQuery,
	opts Options,
	scan SafetyScanner,
	redact Redactor,
) ([]state.MemoryItem, error) {
	if len(items) == 0 {
		return nil, nil
	}

	itemLimit := opts.MaxItemChars
	if query.MaxChars > 0 && query.MaxChars < itemLimit {
		itemLimit = query.MaxChars
	}

	prepared := make([]state.MemoryItem, 0, len(items))
	remaining := opts.MaxChars
	for _, item := range items {
		if item.Status != state.MemoryStatusActive {
			continue
		}

		if scan != nil {
			if err := scan(ctx, item); err != nil {
				return nil, err
			}
		}

		// Redact a copy before truncation so sensitive content does not influence
		// the final prompt text even if it would have been truncated away.
		redacted := item
		if redact != nil {
			var err error
			redacted, err = redact(ctx, item)
			if err != nil {
				return nil, err
			}
		}
		textValue := str.String(redacted.Text)
		redacted.Text = textValue.Trim()
		titleValue := str.String(redacted.Title)
		redacted.Title = titleValue.Trim()
		redacted = truncateItem(redacted, itemLimit)
		if redacted.Title == "" && redacted.Text == "" {
			continue
		}

		itemChars := getItemCharCount(redacted)
		if remaining <= 0 {
			break
		}
		if itemChars > remaining {
			redacted = truncateItem(redacted, remaining)
			itemChars = getItemCharCount(redacted)
		}

		prepared = append(prepared, redacted.Clone())
		remaining -= itemChars
	}

	return prepared, nil
}

// getAutoFileFromRoot performs a case-insensitive lookup so users do not have to
// remember exact filename casing across platforms.
func getAutoFileFromRoot(root string) (string, bool, error) {
	rootValue := str.String(root)
	root = rootValue.Trim()
	if root == "" {
		return "", false, nil
	}

	info, err := stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}

		return "", false, fmt.Errorf("stat profile home %q: %w", root, err)
	}
	if !info.IsDir() {
		return "", false, nil
	}

	entries, err := readDir(root)
	if err != nil {
		return "", false, fmt.Errorf("read profile home %q: %w", root, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.EqualFold(entry.Name(), defaultFileName) {
			return filepath.Join(root, entry.Name()), true, nil
		}
	}

	return "", false, nil
}

// getFileMemoryID makes file-pinned IDs stable across runs and distinct from
// generated store-backed memory IDs.
func getFileMemoryID(file string) string {
	fileValue := str.String(file)
	return "pinned_file:" + fileValue.Trim()
}

func getItemCharCount(item state.MemoryItem) int {
	return len([]rune(item.Title)) + len([]rune(item.Text))
}

// truncateItem spends the item budget on the title first, then the body. A
// title-only item is still useful context; a body without a title is also kept
// when budget remains.
func truncateItem(item state.MemoryItem, maxChars int) state.MemoryItem {
	if maxChars <= 0 {
		item.Title = ""
		item.Text = ""
		return item
	}

	titleRunes := []rune(item.Title)
	if len(titleRunes) >= maxChars {
		item.Title = string(titleRunes[:maxChars])
		item.Text = ""
		return item
	}

	item.Text = truncateRunes(item.Text, maxChars-len(titleRunes))
	return item
}

func truncateRunes(value string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}

	runes := []rune(value)
	if len(runes) <= maxChars {
		return value
	}
	return string(runes[:maxChars])
}
