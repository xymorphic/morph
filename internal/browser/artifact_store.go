package browser

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/rs/zerolog/log"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/pkg/nanoid"
)

const artifactIDPrefix = "artifact_"

const maxArtifactMetadataBytes = 64 << 10

type artifactStore struct {
	root      string
	maxBytes  int64
	maxTotal  int64
	retention time.Duration
	now       func() time.Time
	mu        sync.Mutex
	records   map[string]artifactRecord
	total     int64
}

type artifactRecord struct {
	Artifact Artifact `json:"artifact"`
	Owner    Owner    `json:"owner"`
}

func newArtifactStore(cfg config.BrowserArtifactConfig, now func() time.Time) (*artifactStore, error) {
	if strings.TrimSpace(cfg.Root) == "" || !filepath.IsAbs(cfg.Root) {
		return nil, errors.New("browser artifact root must be absolute")
	}
	if cfg.MaxBytes <= 0 || cfg.MaxTotalBytes < cfg.MaxBytes || cfg.Retention <= 0 {
		return nil, errors.New("browser artifact limits are invalid")
	}
	if now == nil {
		return nil, errors.New("browser artifact clock is required")
	}
	if err := os.MkdirAll(cfg.Root, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(cfg.Root, 0o700); err != nil {
		return nil, err
	}
	store := &artifactStore{
		root: filepath.Clean(cfg.Root), maxBytes: cfg.MaxBytes, maxTotal: cfg.MaxTotalBytes,
		retention: cfg.Retention, now: now, records: make(map[string]artifactRecord),
	}
	if err := store.load(); err != nil {
		return nil, err
	}

	return store, nil
}

func (s *artifactStore) create(owner Owner, profile string, source string, effects []permissions.Effect, value BackendArtifact) (Artifact, error) {
	if s == nil {
		return Artifact{}, errors.New("browser artifact store is required")
	}
	if owner.Actor.ID == "" || owner.Profile == "" || owner.SessionID == "" || strings.TrimSpace(profile) == "" {
		return Artifact{}, errors.New("browser artifact owner is required")
	}
	if value.Kind != ArtifactScreenshot && value.Kind != ArtifactPDF && value.Kind != ArtifactDownload {
		return Artifact{}, errors.New("browser artifact kind is invalid")
	}
	if len(value.Data) == 0 {
		return Artifact{}, errors.New("browser artifact is empty")
	}
	if int64(len(value.Data)) > s.maxBytes {
		return Artifact{}, errors.New("browser artifact exceeds the size limit")
	}

	now := s.now()
	handle := nanoid.MustGenerate(artifactIDPrefix)
	artifact := Artifact{
		Handle: handle, Kind: value.Kind, Name: getSafeArtifactName(value.Name, value.Kind),
		MIMEType: getSafeArtifactMIME(value.MIMEType, value.Kind), Size: int64(len(value.Data)),
		Profile: profile, SessionID: owner.SessionID, RunID: owner.RunID,
		Source: getSafeArtifactSource(source), Effects: normalizeArtifactEffects(effects),
		Sensitive: slices.Contains(effects, permissions.EffectCredentialBearing),
		CreatedAt: now, ExpiresAt: now.Add(s.retention),
	}
	record := artifactRecord{Artifact: artifact, Owner: owner}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.total+artifact.Size > s.maxTotal {
		return Artifact{}, errors.New("browser artifact quota exceeded")
	}
	if err := s.writeRecord(record, value.Data); err != nil {
		return Artifact{}, err
	}
	s.records[handle] = record
	s.total += artifact.Size

	return cloneArtifact(artifact), nil
}

func (s *artifactStore) read(handle string, owner Owner) (ArtifactContent, error) {
	artifact, err := s.metadata(handle, owner)
	if err != nil {
		return ArtifactContent{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	info, err := os.Lstat(s.dataPath(handle))
	if err != nil || !info.Mode().IsRegular() || info.Size() != artifact.Size {
		return ArtifactContent{}, errors.New("browser artifact data is invalid")
	}
	data, err := os.ReadFile(s.dataPath(handle))
	if err != nil {
		return ArtifactContent{}, err
	}
	if int64(len(data)) != artifact.Size || int64(len(data)) > s.maxBytes {
		return ArtifactContent{}, errors.New("browser artifact data is invalid")
	}

	return ArtifactContent{Artifact: artifact, Data: data}, nil
}

func (s *artifactStore) metadata(handle string, owner Owner) (Artifact, error) {
	if s == nil {
		return Artifact{}, errors.New("browser artifact store is required")
	}
	handle = strings.TrimSpace(handle)
	if !isArtifactHandle(handle) {
		return Artifact{}, &Error{Code: ErrorInvalidRequest, Err: errors.New("browser artifact handle is invalid")}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[handle]
	if !ok {
		return Artifact{}, &Error{Code: ErrorNotFound, Err: errors.New("browser artifact not found")}
	}
	if !sameArtifactOwner(record.Owner, owner) {
		return Artifact{}, &Error{Code: ErrorOwnership, Err: errors.New("browser artifact belongs to another owner")}
	}
	if !record.Artifact.ExpiresAt.After(s.now()) {
		return Artifact{}, &Error{Code: ErrorNotReady, Err: errors.New("browser artifact has expired")}
	}
	return cloneArtifact(record.Artifact), nil
}

func (s *artifactStore) cleanup(active func(Owner) bool) error {
	if s == nil {
		return nil
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	var cleanupErrors []error
	for handle, record := range s.records {
		if record.Artifact.ExpiresAt.After(now) || active != nil && active(record.Owner) {
			continue
		}
		if err := os.Remove(s.metadataPath(handle)); err != nil && !os.IsNotExist(err) {
			cleanupErrors = append(cleanupErrors, err)
			continue
		}
		delete(s.records, handle)
		s.total -= record.Artifact.Size
		if err := os.Remove(s.dataPath(handle)); err != nil && !os.IsNotExist(err) {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return errors.Join(append(cleanupErrors, err)...)
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		remove := strings.HasSuffix(entry.Name(), ".part")
		if strings.HasSuffix(entry.Name(), ".bin") {
			handle := strings.TrimSuffix(entry.Name(), ".bin")
			_, known := s.records[handle]
			info, infoErr := entry.Info()
			if infoErr != nil {
				cleanupErrors = append(cleanupErrors, infoErr)
				continue
			}
			remove = !known && !info.ModTime().After(now.Add(-s.retention))
		}
		if remove {
			cleanupErrors = append(cleanupErrors, os.Remove(filepath.Join(s.root, entry.Name())))
		}
	}

	return errors.Join(cleanupErrors...)
}

func (s *artifactStore) load() error {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		handle := strings.TrimSuffix(entry.Name(), ".json")
		if !isArtifactHandle(handle) {
			continue
		}
		metadataInfo, err := entry.Info()
		if err != nil || !metadataInfo.Mode().IsRegular() || metadataInfo.Size() <= 0 ||
			metadataInfo.Size() > maxArtifactMetadataBytes {
			s.discardInvalidRecord(handle, "metadata is invalid")
			continue
		}
		raw, err := os.ReadFile(s.metadataPath(handle))
		if err != nil {
			s.discardInvalidRecord(handle, "metadata is unreadable")
			continue
		}
		var record artifactRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			s.discardInvalidRecord(handle, "metadata is malformed")
			continue
		}
		if !isStoredArtifactValid(record, handle, s.maxBytes) {
			s.discardInvalidRecord(handle, "metadata is inconsistent")
			continue
		}
		info, err := os.Lstat(s.dataPath(handle))
		if err != nil || !info.Mode().IsRegular() || info.Size() != record.Artifact.Size {
			s.discardInvalidRecord(handle, "data is inconsistent")
			continue
		}
		s.records[handle] = record
		s.total += record.Artifact.Size
	}
	s.reconcileLoadedQuota()

	return nil
}

func (s *artifactStore) reconcileLoadedQuota() {
	records := make([]artifactRecord, 0, len(s.records))
	for _, record := range s.records {
		records = append(records, record)
	}
	slices.SortFunc(records, func(left, right artifactRecord) int {
		leftExpired := !left.Artifact.ExpiresAt.After(s.now())
		rightExpired := !right.Artifact.ExpiresAt.After(s.now())
		if leftExpired != rightExpired {
			if leftExpired {
				return -1
			}
			return 1
		}
		return left.Artifact.CreatedAt.Compare(right.Artifact.CreatedAt)
	})
	for _, record := range records {
		if s.total <= s.maxTotal {
			return
		}
		handle := record.Artifact.Handle
		delete(s.records, handle)
		s.total -= record.Artifact.Size
		s.discardInvalidRecord(handle, "record exceeds the configured quota")
	}
}

func (s *artifactStore) discardInvalidRecord(handle, reason string) {
	var removeErrors []error
	for _, path := range []string{s.metadataPath(handle), s.dataPath(handle)} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			removeErrors = append(removeErrors, err)
		}
	}
	log.Warn().
		Err(errors.Join(removeErrors...)).
		Str("artifact_handle", handle).
		Str("reason", reason).
		Msg("Discarded invalid browser artifact record")
}

func (s *artifactStore) writeRecord(record artifactRecord, data []byte) error {
	handle := record.Artifact.Handle
	dataPart := s.dataPath(handle) + ".part"
	metadataPart := s.metadataPath(handle) + ".part"
	if err := writePrivateArtifactFile(dataPart, data); err != nil {
		return err
	}
	if err := os.Link(dataPart, s.dataPath(handle)); err != nil {
		_ = os.Remove(dataPart)
		return err
	}
	if err := os.Remove(dataPart); err != nil {
		_ = os.Remove(s.dataPath(handle))
		return err
	}
	metadata, err := json.Marshal(record)
	if err != nil {
		_ = os.Remove(s.dataPath(handle))
		return err
	}
	if err := writePrivateArtifactFile(metadataPart, metadata); err != nil {
		_ = os.Remove(s.dataPath(handle))
		return err
	}
	if err := os.Link(metadataPart, s.metadataPath(handle)); err != nil {
		_ = os.Remove(metadataPart)
		_ = os.Remove(s.dataPath(handle))
		return err
	}
	if err := os.Remove(metadataPart); err != nil {
		_ = os.Remove(s.metadataPath(handle))
		_ = os.Remove(s.dataPath(handle))
		return err
	}

	return nil
}

func writePrivateArtifactFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	remove = false
	return nil
}

func isStoredArtifactValid(record artifactRecord, handle string, maxBytes int64) bool {
	artifact := record.Artifact
	validKind := artifact.Kind == ArtifactScreenshot || artifact.Kind == ArtifactPDF || artifact.Kind == ArtifactDownload
	return artifact.Handle == handle && validKind && artifact.Size > 0 && artifact.Size <= maxBytes &&
		artifact.Profile != "" && artifact.SessionID == record.Owner.SessionID && artifact.RunID == record.Owner.RunID &&
		!artifact.CreatedAt.IsZero() && artifact.ExpiresAt.After(artifact.CreatedAt) &&
		artifact.Name == getSafeArtifactName(artifact.Name, artifact.Kind) &&
		artifact.MIMEType == getSafeArtifactMIME(artifact.MIMEType, artifact.Kind) &&
		artifact.Source == getSafeArtifactSource(artifact.Source) &&
		isArtifactEffectsValid(artifact.Effects) &&
		artifact.Sensitive == slices.Contains(artifact.Effects, permissions.EffectCredentialBearing) &&
		record.Owner.Actor.ID != "" && record.Owner.Profile != "" && record.Owner.SessionID != ""
}

func isArtifactEffectsValid(effects []permissions.Effect) bool {
	_, err := (permissions.Operation{
		Resource: permissions.ResourceBrowser,
		Action:   permissions.ActionRead,
		Effects:  effects,
	}).Normalize()
	return err == nil && slices.Equal(effects, normalizeArtifactEffects(effects))
}

func (s *artifactStore) dataPath(handle string) string {
	return filepath.Join(s.root, handle+".bin")
}

func (s *artifactStore) metadataPath(handle string) string {
	return filepath.Join(s.root, handle+".json")
}

func isArtifactHandle(handle string) bool {
	if !strings.HasPrefix(handle, artifactIDPrefix) || len(handle) <= len(artifactIDPrefix) {
		return false
	}
	for _, value := range handle[len(artifactIDPrefix):] {
		if !unicode.IsLetter(value) && !unicode.IsDigit(value) && value != '_' && value != '-' {
			return false
		}
	}
	return true
}

func sameArtifactOwner(left, right Owner) bool {
	return left.Actor == right.Actor && left.Profile == right.Profile && left.SessionID == right.SessionID
}

func getSafeArtifactName(name string, kind ArtifactKind) string {
	if kind == ArtifactScreenshot {
		return "screenshot.png"
	}
	if kind == ArtifactPDF {
		return "page.pdf"
	}
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.Map(func(value rune) rune {
		if unicode.IsControl(value) {
			return -1
		}
		return value
	}, name)
	if name == "" || name == "." {
		name = "download"
	}
	if len(name) > 255 {
		name = name[:255]
	}
	return name
}

func getSafeArtifactMIME(value string, kind ArtifactKind) string {
	value = strings.ToLower(strings.TrimSpace(strings.SplitN(value, ";", 2)[0]))
	if kind == ArtifactScreenshot {
		return "image/png"
	}
	if kind == ArtifactPDF {
		return "application/pdf"
	}
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return "application/octet-stream"
	}
	return value
}

func getSafeArtifactSource(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func normalizeArtifactEffects(effects []permissions.Effect) []permissions.Effect {
	result := append([]permissions.Effect(nil), effects...)
	slices.Sort(result)
	return slices.Compact(result)
}

func cloneArtifact(artifact Artifact) Artifact {
	artifact.Effects = append([]permissions.Effect(nil), artifact.Effects...)
	return artifact
}
