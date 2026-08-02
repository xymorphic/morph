package nanoid

import (
	"fmt"
	"regexp"
	"strings"

	gonanoid "github.com/matoous/go-nanoid/v2"
	"github.com/xymorphic/morph/pkg/str"
)

const (
	DefaultLength = 21
	Alphabet      = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
)

var (
	generate      = gonanoid.Generate
	prefixPattern = regexp.MustCompile(`^[A-Za-z0-9]+_$`)
)

func Generate(prefix string, length ...int) (string, error) {
	resolvedLength, err := getLength(length)
	if err != nil {
		return "", err
	}

	if err := checkPrefix(prefix); err != nil {
		return "", err
	}

	id, err := generate(Alphabet, resolvedLength)
	if err != nil {
		return "", err
	}

	return prefix + id, nil
}

func MustGenerate(prefix string, length ...int) string {
	id, err := Generate(prefix, length...)
	if err != nil {
		panic(err)
	}

	return id
}

func FromSeed(prefix string, seed string, fallback string) (string, error) {
	if err := checkPrefix(prefix); err != nil {
		return "", err
	}

	normalized := normalizeAlphanumeric(seed)
	if normalized == "" {
		normalized = normalizeAlphanumeric(fallback)
	}
	if normalized == "" {
		return "", fmt.Errorf("nanoid seed fallback must contain alphanumeric characters")
	}

	var builder strings.Builder
	builder.Grow(len(prefix) + DefaultLength)
	builder.WriteString(prefix)
	for builder.Len()-len(prefix) < DefaultLength {
		remaining := DefaultLength - (builder.Len() - len(prefix))
		if remaining >= len(normalized) {
			builder.WriteString(normalized)
			continue
		}
		builder.WriteString(normalized[:remaining])
	}

	return builder.String(), nil
}

func MustFromSeed(prefix string, seed string, fallback string) string {
	id, err := FromSeed(prefix, seed, fallback)
	if err != nil {
		panic(err)
	}

	return id
}

func IsValidID(id string) bool {
	return ValidateID(id) == nil
}

func ValidateID(id string) error {
	idValue := str.String(id)
	id = idValue.Trim()
	if id == "" {
		return fmt.Errorf("prefixed nanoid is required")
	}

	underscore := strings.IndexByte(id, '_')
	if underscore <= 0 || underscore != strings.LastIndexByte(id, '_') {
		return fmt.Errorf("prefixed nanoid must contain a single underscore separator")
	}

	prefix := id[:underscore+1]
	if err := checkPrefix(prefix); err != nil {
		return err
	}

	suffix := id[underscore+1:]
	if len(suffix) != DefaultLength {
		return fmt.Errorf("prefixed nanoid suffix must be %d characters", DefaultLength)
	}

	for i := 0; i < len(suffix); i++ {
		if !strings.ContainsRune(Alphabet, rune(suffix[i])) {
			return fmt.Errorf("prefixed nanoid suffix must be alphanumeric")
		}
	}

	return nil
}

func normalizeAlphanumeric(value string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= 'a' && r <= 'z':
			return r
		default:
			return -1
		}
	}, value)
}

func getLength(length []int) (int, error) {
	if len(length) == 0 {
		return DefaultLength, nil
	}

	if len(length) > 1 {
		return 0, fmt.Errorf("nanoid length accepts at most one value")
	}

	if length[0] <= 0 {
		return 0, fmt.Errorf("nanoid length must be greater than zero")
	}

	return length[0], nil
}

func checkPrefix(prefix string) error {
	if prefix == "" {
		return nil
	}

	if !prefixPattern.MatchString(prefix) {
		return fmt.Errorf("nanoid prefix must be alphanumeric with a single trailing underscore")
	}

	return nil
}
