package browser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/permissions"
)

func TestArtifactStore_PersistsBoundedRedactedOwnerScopedArtifacts(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	store, err := newArtifactStore(config.BrowserArtifactConfig{
		Root: root, MaxBytes: 32, MaxTotalBytes: 64, Retention: time.Hour,
	}, func() time.Time { return now })
	require.NoError(t, err)
	owner := Owner{
		Actor:   permissions.Actor{Kind: permissions.ActorLocalOwner, ID: "owner"},
		Profile: "default", SessionID: "session", RunID: "run",
	}
	artifact, err := store.create(
		owner,
		"isolated",
		"https://user:password@example.com/report?token=secret#private",
		[]permissions.Effect{permissions.EffectRead, permissions.EffectCredentialBearing, permissions.EffectRead},
		BackendArtifact{
			Kind: ArtifactScreenshot, Name: "../screen\nshot.bad", MIMEType: "text/plain", Data: []byte("png"),
		},
	)
	require.NoError(t, err)
	require.Equal(t, "screenshot.png", artifact.Name)
	require.Equal(t, "image/png", artifact.MIMEType)
	require.Equal(t, "https://example.com/report", artifact.Source)
	require.Equal(t, []permissions.Effect{permissions.EffectCredentialBearing, permissions.EffectRead}, artifact.Effects)
	require.True(t, artifact.Sensitive)
	require.Equal(t, owner.RunID, artifact.RunID)

	content, err := store.read(artifact.Handle, owner)
	require.NoError(t, err)
	require.Equal(t, []byte("png"), content.Data)
	raw, err := json.Marshal(content)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "cG5n")
	require.NotContains(t, string(raw), `"Data"`)
	content.Artifact.Effects[0] = permissions.EffectWrite
	metadata, err := store.metadata(artifact.Handle, owner)
	require.NoError(t, err)
	require.Equal(t, permissions.EffectCredentialBearing, metadata.Effects[0])

	_, err = store.read(artifact.Handle, Owner{
		Actor: owner.Actor, Profile: owner.Profile, SessionID: "other",
	})
	require.EqualError(t, err, "browser artifact belongs to another owner")
	require.FileExists(t, filepath.Join(root, artifact.Handle+".bin"))
	require.FileExists(t, filepath.Join(root, artifact.Handle+".json"))
	info, err := os.Stat(filepath.Join(root, artifact.Handle+".bin"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	reloaded, err := newArtifactStore(config.BrowserArtifactConfig{
		Root: root, MaxBytes: 32, MaxTotalBytes: 64, Retention: time.Hour,
	}, func() time.Time { return now })
	require.NoError(t, err)
	reloadedContent, err := reloaded.read(artifact.Handle, owner)
	require.NoError(t, err)
	require.Equal(t, content.Data, reloadedContent.Data)
}

func TestArtifactStore_EnforcesSizeQuotaExpiryAndActiveOwnerRetention(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	store, err := newArtifactStore(config.BrowserArtifactConfig{
		Root: t.TempDir(), MaxBytes: 4, MaxTotalBytes: 6, Retention: time.Minute,
	}, func() time.Time { return now })
	require.NoError(t, err)
	owner := Owner{
		Actor:   permissions.Actor{Kind: permissions.ActorLocalOwner, ID: "owner"},
		Profile: "default", SessionID: "session",
	}
	first, err := store.create(owner, "isolated", "https://example.com", nil, BackendArtifact{
		Kind: ArtifactPDF, Data: []byte("1234"),
	})
	require.NoError(t, err)
	_, err = store.create(owner, "isolated", "https://example.com", nil, BackendArtifact{
		Kind: ArtifactDownload, Data: []byte("123"),
	})
	require.EqualError(t, err, "browser artifact quota exceeded")
	_, err = store.create(owner, "isolated", "https://example.com", nil, BackendArtifact{
		Kind: ArtifactDownload, Data: []byte("12345"),
	})
	require.EqualError(t, err, "browser artifact exceeds the size limit")

	now = now.Add(2 * time.Minute)
	_, err = store.read(first.Handle, owner)
	require.EqualError(t, err, "browser artifact has expired")
	require.NoError(t, store.cleanup(func(candidate Owner) bool { return sameArtifactOwner(candidate, owner) }))
	require.FileExists(t, store.dataPath(first.Handle))
	require.NoError(t, os.WriteFile(filepath.Join(store.root, "abandoned.part"), []byte("partial"), 0o600))
	orphan := filepath.Join(store.root, "artifact_orphan.bin")
	require.NoError(t, os.WriteFile(orphan, []byte("orphan"), 0o600))
	require.NoError(t, os.Chtimes(orphan, now.Add(-2*time.Minute), now.Add(-2*time.Minute)))
	require.NoError(t, store.cleanup(nil))
	require.NoFileExists(t, store.dataPath(first.Handle))
	require.NoFileExists(t, filepath.Join(store.root, "abandoned.part"))
	require.NoFileExists(t, orphan)
}

func TestArtifactStore_RejectsInvalidConfigurationAndRecords(t *testing.T) {
	valid := config.BrowserArtifactConfig{
		Root: t.TempDir(), MaxBytes: 4, MaxTotalBytes: 8, Retention: time.Minute,
	}
	_, err := newArtifactStore(config.BrowserArtifactConfig{}, time.Now)
	require.EqualError(t, err, "browser artifact root must be absolute")
	invalidLimits := valid
	invalidLimits.MaxTotalBytes = 1
	_, err = newArtifactStore(invalidLimits, time.Now)
	require.EqualError(t, err, "browser artifact limits are invalid")
	_, err = newArtifactStore(valid, nil)
	require.EqualError(t, err, "browser artifact clock is required")

	store, err := newArtifactStore(valid, time.Now)
	require.NoError(t, err)
	owner := Owner{
		Actor:   permissions.Actor{Kind: permissions.ActorLocalOwner, ID: "owner"},
		Profile: "default", SessionID: "session",
	}
	_, err = store.create(Owner{}, "isolated", "", nil, BackendArtifact{Kind: ArtifactPDF, Data: []byte("x")})
	require.EqualError(t, err, "browser artifact owner is required")
	_, err = store.create(owner, "isolated", "", nil, BackendArtifact{Kind: "video", Data: []byte("x")})
	require.EqualError(t, err, "browser artifact kind is invalid")
	_, err = store.create(owner, "isolated", "", nil, BackendArtifact{Kind: ArtifactPDF})
	require.EqualError(t, err, "browser artifact is empty")
	_, err = store.read("../escape", owner)
	require.EqualError(t, err, "browser artifact handle is invalid")
	_, err = store.read("artifact_missing", owner)
	require.EqualError(t, err, "browser artifact not found")
	_, err = (*artifactStore)(nil).create(owner, "isolated", "", nil, BackendArtifact{
		Kind: ArtifactPDF, Data: []byte("x"),
	})
	require.EqualError(t, err, "browser artifact store is required")
	_, err = (*artifactStore)(nil).metadata("artifact_missing", owner)
	require.EqualError(t, err, "browser artifact store is required")
	require.NoError(t, (*artifactStore)(nil).cleanup(nil))

	artifact, err := store.create(owner, "isolated", "%", nil, BackendArtifact{
		Kind: ArtifactDownload, Name: ".", MIMEType: "text/plain\r\nunsafe", Data: []byte("x"),
	})
	require.NoError(t, err)
	require.Equal(t, "download", artifact.Name)
	require.Equal(t, "application/octet-stream", artifact.MIMEType)
	require.Empty(t, artifact.Source)
	require.False(t, isArtifactHandle("artifact_bad/path"))
}

func TestArtifactStore_DetectsChangedDataAndInvalidPersistentMetadata(t *testing.T) {
	owner := Owner{
		Actor:   permissions.Actor{Kind: permissions.ActorLocalOwner, ID: "owner"},
		Profile: "default", SessionID: "session",
	}
	cfg := config.BrowserArtifactConfig{
		Root: t.TempDir(), MaxBytes: 64, MaxTotalBytes: 128, Retention: time.Minute,
	}
	store, err := newArtifactStore(cfg, time.Now)
	require.NoError(t, err)
	artifact, err := store.create(owner, "isolated", "https://example.com", nil, BackendArtifact{
		Kind: ArtifactDownload, Data: []byte("stable"),
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(store.dataPath(artifact.Handle), []byte("changed"), 0o600))
	_, err = store.read(artifact.Handle, owner)
	require.EqualError(t, err, "browser artifact data is invalid")

	tests := []struct {
		name     string
		metadata string
		data     string
	}{
		{name: "invalid JSON", metadata: `{`, data: "x"},
		{name: "inconsistent handle", metadata: `{"artifact":{"handle":"artifact_other","size":1}}`, data: "x"},
		{name: "missing data", metadata: `{"artifact":{"handle":"artifact_record","size":1}}`},
		{name: "missing owner", metadata: `{"artifact":{"handle":"artifact_record","kind":"pdf","size":1,"profile":"isolated","session_id":"session","created_at":"2026-07-19T12:00:00Z","expires_at":"2026-07-19T13:00:00Z"}}`, data: "x"},
		{name: "owner mismatch", metadata: `{"artifact":{"handle":"artifact_record","kind":"pdf","name":"page.pdf","mime_type":"application/pdf","size":1,"profile":"isolated","session_id":"other","created_at":"2026-07-19T12:00:00Z","expires_at":"2026-07-19T13:00:00Z"},"owner":{"Actor":{"Kind":"local_owner","ID":"owner"},"Profile":"default","SessionID":"session"}}`, data: "x"},
		{name: "invalid effect", metadata: `{"artifact":{"handle":"artifact_record","kind":"pdf","name":"page.pdf","mime_type":"application/pdf","size":1,"profile":"isolated","session_id":"session","effects":["unknown"],"created_at":"2026-07-19T12:00:00Z","expires_at":"2026-07-19T13:00:00Z"},"owner":{"Actor":{"Kind":"local_owner","ID":"owner"},"Profile":"default","SessionID":"session"}}`, data: "x"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			handle := "artifact_record"
			require.NoError(t, os.WriteFile(filepath.Join(root, handle+".json"), []byte(test.metadata), 0o600))
			if test.data != "" {
				require.NoError(t, os.WriteFile(filepath.Join(root, handle+".bin"), []byte(test.data), 0o600))
			}
			recovered, err := newArtifactStore(config.BrowserArtifactConfig{
				Root: root, MaxBytes: 64, MaxTotalBytes: 128, Retention: time.Minute,
			}, time.Now)
			require.NoError(t, err)
			require.Empty(t, recovered.records)
			require.NoFileExists(t, filepath.Join(root, handle+".json"))
			require.NoFileExists(t, filepath.Join(root, handle+".bin"))
		})
	}
}

func TestArtifactStore_ReconcilesReducedQuotaByRemovingOldestRecords(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	owner := Owner{
		Actor:   permissions.Actor{Kind: permissions.ActorLocalOwner, ID: "owner"},
		Profile: "default", SessionID: "session",
	}
	store, err := newArtifactStore(config.BrowserArtifactConfig{
		Root: root, MaxBytes: 4, MaxTotalBytes: 8, Retention: time.Hour,
	}, func() time.Time { return now })
	require.NoError(t, err)
	oldest, err := store.create(owner, "isolated", "", nil, BackendArtifact{
		Kind: ArtifactPDF, Data: []byte("1234"),
	})
	require.NoError(t, err)
	now = now.Add(time.Minute)
	newest, err := store.create(owner, "isolated", "", nil, BackendArtifact{
		Kind: ArtifactPDF, Data: []byte("5678"),
	})
	require.NoError(t, err)

	recovered, err := newArtifactStore(config.BrowserArtifactConfig{
		Root: root, MaxBytes: 4, MaxTotalBytes: 4, Retention: time.Hour,
	}, func() time.Time { return now })
	require.NoError(t, err)
	require.Equal(t, int64(4), recovered.total)
	_, err = recovered.read(oldest.Handle, owner)
	require.EqualError(t, err, "browser artifact not found")
	content, err := recovered.read(newest.Handle, owner)
	require.NoError(t, err)
	require.Equal(t, []byte("5678"), content.Data)
}

func TestWritePrivateArtifactFile_DoesNotFollowOrOverwriteExistingPaths(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "artifact.part")
	require.NoError(t, writePrivateArtifactFile(path, []byte("new")))
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, []byte("new"), content)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	err = writePrivateArtifactFile(path, []byte("replacement"))
	require.ErrorIs(t, err, os.ErrExist)
	content, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, []byte("new"), content)

	target := filepath.Join(root, "target")
	require.NoError(t, os.WriteFile(target, []byte("target"), 0o600))
	link := filepath.Join(root, "link.part")
	require.NoError(t, os.Symlink(target, link))
	err = writePrivateArtifactFile(link, []byte("replacement"))
	require.ErrorIs(t, err, os.ErrExist)
	targetContent, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, []byte("target"), targetContent)
}
