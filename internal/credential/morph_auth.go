package credential

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	morphauth "github.com/wandxy/morph/internal/auth"
)

type MorphAuthRecord struct {
	IdentityID string               `json:"identityId"`
	Generation uint64               `json:"generation"`
	PrivateKey string               `json:"privateKey"`
	Token      string               `json:"token,omitempty"`
	UpdatedAt  string               `json:"updatedAt"`
	Pending    *MorphIdentityRecord `json:"pendingIdentity,omitempty"`
}

type MorphIdentityRecord struct {
	IdentityID string `json:"identityId"`
	Generation uint64 `json:"generation"`
	PrivateKey string `json:"privateKey"`
	PreparedAt string `json:"preparedAt"`
}

func (s *FileStore) LoadMorphAuth() (MorphAuthRecord, bool, error) {
	var record MorphAuthRecord
	var found bool
	err := s.withLockedDocument(false, func(document rawDocument) (bool, error) {
		raw, ok := document[morphAuthDocumentKey]
		if !ok {
			return false, nil
		}
		if err := json.Unmarshal(raw, &record); err != nil {
			return false, fmt.Errorf("parse Morph auth record: %w", err)
		}
		if err := checkMorphAuthRecord(record); err != nil {
			return false, err
		}
		found = true
		return false, nil
	})

	return record, found, err
}

func (s *FileStore) SetMorphAuth(record MorphAuthRecord) error {
	if err := checkMorphAuthRecord(record); err != nil {
		return err
	}
	return s.withLockedDocument(true, func(document rawDocument) (bool, error) {
		body, err := json.Marshal(record)
		if err != nil {
			return false, fmt.Errorf("encode Morph auth record: %w", err)
		}
		document[morphAuthDocumentKey] = body
		return true, nil
	})
}

func (s *FileStore) LoadOrCreateIdentity() (morphauth.Identity, error) {
	var identity morphauth.Identity
	err := s.withLockedDocument(true, func(document rawDocument) (bool, error) {
		if raw, ok := document[morphAuthDocumentKey]; ok {
			var record MorphAuthRecord
			if err := json.Unmarshal(raw, &record); err != nil {
				return false, fmt.Errorf("parse Morph auth record: %w", err)
			}
			if err := checkMorphAuthRecord(record); err != nil {
				return false, err
			}
			var err error
			identity, err = morphauth.ParseIdentity([]byte(record.PrivateKey), record.Generation)
			if err != nil {
				return false, err
			}
			if identity.ID != record.IdentityID {
				return false, errors.New("stored Morph identity does not match its private key")
			}
			return false, nil
		}

		var err error
		identity, err = morphauth.GenerateIdentity(1)
		if err != nil {
			return false, err
		}
		privateKey, err := morphauth.MarshalIdentity(identity)
		if err != nil {
			return false, err
		}
		record := MorphAuthRecord{
			IdentityID: identity.ID,
			Generation: identity.Generation,
			PrivateKey: string(privateKey),
			UpdatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
		}
		body, err := json.Marshal(record)
		if err != nil {
			return false, fmt.Errorf("encode Morph auth record: %w", err)
		}
		document[morphAuthDocumentKey] = body

		return true, nil
	})

	return identity, err
}

func (s *FileStore) RotateIdentity() (morphauth.Identity, error) {
	_, identity, err := s.PrepareIdentityRotation()
	if err != nil {
		return morphauth.Identity{}, err
	}
	if err := s.ActivateIdentityRotation(identity.ID); err != nil {
		return morphauth.Identity{}, err
	}

	return identity, nil
}

func (s *FileStore) PrepareIdentityRotation() (MorphAuthRecord, morphauth.Identity, error) {
	var current MorphAuthRecord
	var identity morphauth.Identity
	err := s.withLockedDocument(false, func(document rawDocument) (bool, error) {
		raw, ok := document[morphAuthDocumentKey]
		if !ok {
			return false, errors.New("morph identity is not initialized")
		}
		if err := json.Unmarshal(raw, &current); err != nil {
			return false, fmt.Errorf("parse Morph auth record: %w", err)
		}
		if err := checkMorphAuthRecord(current); err != nil {
			return false, err
		}
		if current.Pending != nil {
			return false, errors.New("morph identity rotation is already pending")
		}
		var err error
		identity, err = morphauth.GenerateIdentity(current.Generation + 1)
		if err != nil {
			return false, err
		}
		privateKey, err := morphauth.MarshalIdentity(identity)
		if err != nil {
			return false, err
		}
		current.Pending = &MorphIdentityRecord{
			IdentityID: identity.ID,
			Generation: identity.Generation,
			PrivateKey: string(privateKey),
			PreparedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
		body, err := json.Marshal(current)
		if err != nil {
			return false, fmt.Errorf("encode Morph auth record: %w", err)
		}
		document[morphAuthDocumentKey] = body

		return true, nil
	})

	return current, identity, err
}

func (s *FileStore) ActivateIdentityRotation(identityID string) error {
	return s.withLockedDocument(false, func(document rawDocument) (bool, error) {
		record, err := loadMorphAuthRecord(document)
		if err != nil {
			return false, err
		}
		if record.Pending == nil || record.Pending.IdentityID != strings.TrimSpace(identityID) {
			return false, errors.New("matching pending Morph identity rotation is required")
		}
		record.IdentityID = record.Pending.IdentityID
		record.Generation = record.Pending.Generation
		record.PrivateKey = record.Pending.PrivateKey
		record.Token = ""
		record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		record.Pending = nil

		return setMorphAuthRecord(document, record)
	})
}

func (s *FileStore) AbortIdentityRotation(identityID string) error {
	return s.withLockedDocument(false, func(document rawDocument) (bool, error) {
		record, err := loadMorphAuthRecord(document)
		if err != nil {
			return false, err
		}
		if record.Pending == nil {
			return false, nil
		}
		if identityID != "" && record.Pending.IdentityID != strings.TrimSpace(identityID) {
			return false, errors.New("pending Morph identity rotation does not match")
		}
		record.Pending = nil

		return setMorphAuthRecord(document, record)
	})
}

func loadMorphAuthRecord(document rawDocument) (MorphAuthRecord, error) {
	raw, ok := document[morphAuthDocumentKey]
	if !ok {
		return MorphAuthRecord{}, errors.New("morph identity is not initialized")
	}
	var record MorphAuthRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return MorphAuthRecord{}, fmt.Errorf("parse Morph auth record: %w", err)
	}
	if err := checkMorphAuthRecord(record); err != nil {
		return MorphAuthRecord{}, err
	}

	return record, nil
}

func setMorphAuthRecord(document rawDocument, record MorphAuthRecord) (bool, error) {
	body, err := json.Marshal(record)
	if err != nil {
		return false, fmt.Errorf("encode Morph auth record: %w", err)
	}
	document[morphAuthDocumentKey] = body

	return true, nil
}

func (s *FileStore) withLockedDocument(
	create bool,
	fn func(rawDocument) (bool, error),
) error {
	if s == nil {
		return errors.New("credential store is required")
	}
	path := strings.TrimSpace(s.Path)
	if path == "" {
		path = DefaultPath()
	}
	if !create {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			_, callbackErr := fn(make(rawDocument))
			return callbackErr
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create credential dir: %w", err)
	}
	release, err := acquireFileLock(path + ".lock")
	if err != nil {
		return err
	}
	defer release()

	document, err := loadDocument(path)
	if err != nil {
		return err
	}
	changed, err := fn(document)
	if err != nil {
		return err
	}
	if changed || create {
		return writeDocument(path, document)
	}

	return nil
}

func checkMorphAuthRecord(record MorphAuthRecord) error {
	if strings.TrimSpace(record.IdentityID) == "" || record.Generation == 0 ||
		strings.TrimSpace(record.PrivateKey) == "" {
		return errors.New("morph auth record requires identity, generation, and private key")
	}
	identity, err := morphauth.ParseIdentity([]byte(record.PrivateKey), record.Generation)
	if err != nil {
		return err
	}
	if identity.ID != record.IdentityID {
		return errors.New("stored Morph identity does not match its private key")
	}
	if record.Pending != nil {
		if record.Pending.Generation <= record.Generation ||
			strings.TrimSpace(record.Pending.PreparedAt) == "" {
			return errors.New("pending Morph identity rotation is invalid")
		}
		pending, err := morphauth.ParseIdentity(
			[]byte(record.Pending.PrivateKey), record.Pending.Generation,
		)
		if err != nil || pending.ID != record.Pending.IdentityID {
			return errors.New("pending Morph identity does not match its private key")
		}
	}

	return nil
}
