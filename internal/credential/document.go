package credential

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/xymorphic/morph/pkg/str"
)

const morphAuthDocumentKey = "_morph"

type rawDocument map[string]json.RawMessage

func loadDocument(path string) (rawDocument, error) {
	info, statErr := os.Lstat(path)
	if statErr == nil && !info.IsDir() {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("credential store must not be a symbolic link")
		}
		if err := checkCredentialPermissions(path); err != nil {
			return nil, err
		}
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(rawDocument), nil
		}

		return nil, fmt.Errorf("read credential store: %w", err)
	}
	if str.String(string(body)).Trim() == "" {
		return make(rawDocument), nil
	}

	var document rawDocument
	if err := json.Unmarshal(body, &document); err != nil {
		return nil, fmt.Errorf("parse credential store: %w", err)
	}
	if document == nil {
		document = make(rawDocument)
	}

	return document, nil
}

func writeDocument(path string, document rawDocument) error {
	body, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode credential store: %w", err)
	}
	body = append(body, '\n')
	if _, err := os.Stat(filepath.Dir(path)); err == nil {
		if err := protectCredentialDirectory(filepath.Dir(path)); err != nil {
			return fmt.Errorf("secure credential store directory: %w", err)
		}
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create credential store temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write credential store temp file: %w", err)
	}
	if err := protectCredentialFile(tmpPath); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure credential store temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync credential store temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close credential store temp file: %w", err)
	}
	if err := replaceCredentialFile(tmpPath, path); err != nil {
		return fmt.Errorf("replace credential store: %w", err)
	}
	if err := protectCredentialFile(path); err != nil {
		return fmt.Errorf("secure credential store: %w", err)
	}
	if err := syncCredentialDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync credential store directory: %w", err)
	}

	return nil
}

func providersFromDocument(document rawDocument) (map[string]StoredCredential, error) {
	providers := make(map[string]StoredCredential, len(document))
	for provider, raw := range document {
		if provider == morphAuthDocumentKey {
			continue
		}
		provider = normalizeProvider(provider)
		if provider == "" {
			continue
		}
		var credential StoredCredential
		if err := json.Unmarshal(raw, &credential); err != nil {
			return nil, fmt.Errorf("parse provider credential %q: %w", provider, err)
		}
		providers[provider] = normalizeCredential(credential)
	}

	return providers, nil
}

func replaceProviders(document rawDocument, providers map[string]StoredCredential) (rawDocument, error) {
	if document == nil {
		document = make(rawDocument)
	}
	for key := range document {
		if key != morphAuthDocumentKey {
			delete(document, key)
		}
	}
	for provider, credential := range providers {
		body, err := json.Marshal(credential)
		if err != nil {
			return nil, fmt.Errorf("encode provider credential %q: %w", provider, err)
		}
		document[provider] = body
	}

	return document, nil
}
