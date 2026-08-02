package docker

import (
	"errors"
	"os"
	"strings"

	"github.com/xymorphic/morph/internal/guardrails"
)

type SecretReference struct {
	Name string
	Env  string
}

type ResolvedSecrets struct {
	Names    []string
	Values   map[string]string
	Redactor *guardrails.ExactValueStream
}

type SecretResolver struct {
	references map[string]SecretReference
}

func NewSecretResolver(references []SecretReference) (*SecretResolver, error) {
	resolver := &SecretResolver{references: make(map[string]SecretReference, len(references))}
	for _, reference := range references {
		reference.Name = strings.TrimSpace(strings.ToLower(reference.Name))
		reference.Env = strings.TrimSpace(reference.Env)
		if reference.Name == "" || reference.Env == "" {
			return nil, errors.New("execution secret reference is incomplete")
		}
		if _, exists := resolver.references[reference.Name]; exists {
			return nil, errors.New("execution secret reference names must be unique")
		}
		resolver.references[reference.Name] = reference
	}
	return resolver, nil
}

func (r *SecretResolver) Resolve(names []string) (ResolvedSecrets, error) {
	resolved := ResolvedSecrets{
		Values:   map[string]string{},
		Redactor: guardrails.NewExactValueStream(),
	}
	for _, name := range names {
		name = strings.TrimSpace(strings.ToLower(name))
		reference, ok := r.references[name]
		if !ok {
			return ResolvedSecrets{}, errors.New("execution secret reference is not configured")
		}
		value, ok := os.LookupEnv(reference.Env)
		if !ok || value == "" {
			return ResolvedSecrets{}, errors.New("execution secret value is unavailable")
		}
		resolved.Names = append(resolved.Names, name)
		resolved.Values[name] = value
		resolved.Redactor.Register(value)
	}
	return resolved, nil
}
