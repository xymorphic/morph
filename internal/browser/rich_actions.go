package browser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/permissions"
)

var (
	publishArtifactExport              = os.Link
	checkArtifactExportLinkUnsupported = isArtifactExportLinkUnsupported
	writeArtifactExportContent         = writeAndSyncArtifactExportContent
	removeArtifactExportTemporary      = os.Remove
)

func (s *Service) Screenshot(ctx context.Context, request ActionRequest) (Artifact, error) {
	return s.captureArtifact(ctx, ActionScreenshot, request, func(
		ctx context.Context, backend RichBackendSession, tabID string,
	) (BackendArtifact, error) {
		return backend.Screenshot(ctx, tabID, request.FullPage)
	})
}

func (s *Service) PDF(ctx context.Context, request ActionRequest) (Artifact, error) {
	return s.captureArtifact(ctx, ActionPDF, request, func(
		ctx context.Context, backend RichBackendSession, tabID string,
	) (BackendArtifact, error) {
		return backend.PDF(ctx, tabID)
	})
}

func (s *Service) Console(ctx context.Context, request ActionRequest) ([]ConsoleMessage, error) {
	runtime, _, backend, err := s.getRichRuntime(ctx, request.SessionID, ActionConsole)
	if err != nil {
		return nil, err
	}
	runtime.actionMu.Lock()
	defer runtime.actionMu.Unlock()
	if err := s.authorizeAction(ctx, ActionConsole, request, false); err != nil {
		return nil, err
	}
	tab, err := s.getTab(ctx, runtime, request.TabID)
	if err != nil {
		return nil, err
	}
	messages, err := backend.Console(ctx, tab.ID, request.Limit)
	if err != nil {
		return nil, getActionError(ActionConsole, err)
	}
	s.touchRuntime(runtime)
	return getSafeConsoleMessages(messages, request.Limit), nil
}

func (s *Service) Upload(ctx context.Context, request ActionRequest) (Tab, error) {
	runtime, interactive, backend, err := s.getRichRuntime(ctx, request.SessionID, ActionUpload)
	if err != nil {
		return Tab{}, err
	}
	if err := checkManagedFileActionProfile(runtime, ActionUpload); err != nil {
		return Tab{}, err
	}
	runtime.actionMu.Lock()
	defer runtime.actionMu.Unlock()
	if err := s.authorizeAction(ctx, ActionUpload, request, false); err != nil {
		return Tab{}, err
	}
	tab, err := s.getTab(ctx, runtime, request.TabID)
	if err != nil {
		return Tab{}, err
	}
	nodeID, err := getReference(tab, request.Ref)
	if err != nil {
		return Tab{}, err
	}
	temporaryRoot, err := filepath.EvalSymlinks(s.cfg.TemporaryRoot)
	if err != nil {
		return Tab{}, getActionError(ActionUpload, err)
	}
	stagingRoot := filepath.Join(temporaryRoot, "uploads", runtime.ID)
	defer func() { _ = os.RemoveAll(stagingRoot) }()
	actionCtx, finishAction, err := s.prepareNetworkAction(ctx, runtime, interactive, ActionUpload, tab.ID)
	if err != nil {
		return Tab{}, getActionError(ActionUpload, err)
	}
	defer finishAction()
	staged, err := stageUpload(actionCtx, request.Path, stagingRoot, s.cfg.Artifacts.MaxBytes)
	if err != nil {
		return Tab{}, getActionError(ActionUpload, err)
	}
	if err := backend.Upload(actionCtx, tab.ID, nodeID, staged.Path); err != nil {
		return Tab{}, getActionError(ActionUpload, err)
	}
	if err := waitForNetworkSettlement(actionCtx, interactive, tab.ID); err != nil {
		return Tab{}, getActionError(ActionUpload, err)
	}
	s.bumpTabGeneration(runtime, tab.ID)
	s.touchRuntime(runtime)
	return s.getTabCopy(runtime, tab.ID), nil
}

func (s *Service) Download(ctx context.Context, request ActionRequest) (Artifact, error) {
	runtime, interactive, backend, err := s.getRichRuntime(ctx, request.SessionID, ActionDownload)
	if err != nil {
		return Artifact{}, err
	}
	if err := checkManagedFileActionProfile(runtime, ActionDownload); err != nil {
		return Artifact{}, err
	}
	runtime.actionMu.Lock()
	defer runtime.actionMu.Unlock()
	operations, err := s.resolveOperations(ctx, ActionDownload, request, false)
	if err != nil {
		return Artifact{}, err
	}
	if err := s.checkOperations(ctx, operations); err != nil {
		return Artifact{}, err
	}
	tab, err := s.getTab(ctx, runtime, request.TabID)
	if err != nil {
		return Artifact{}, err
	}
	nodeID, err := getReference(tab, request.Ref)
	if err != nil {
		return Artifact{}, err
	}
	actionCtx, finishAction, err := s.prepareNetworkAction(ctx, runtime, interactive, ActionDownload, tab.ID)
	if err != nil {
		return Artifact{}, getActionError(ActionDownload, err)
	}
	defer finishAction()
	value, err := backend.Download(actionCtx, tab.ID, nodeID, s.cfg.Artifacts.MaxBytes)
	if err != nil {
		return Artifact{}, getActionError(ActionDownload, err)
	}
	if err := waitForNetworkSettlement(actionCtx, interactive, tab.ID); err != nil {
		return Artifact{}, getActionError(ActionDownload, err)
	}
	if value.Kind != ArtifactDownload {
		return Artifact{}, errors.New("browser backend returned the wrong artifact kind")
	}
	artifact, err := s.storeArtifact(runtime, tab, operations, value)
	if err != nil {
		return Artifact{}, err
	}
	s.touchRuntime(runtime)
	return artifact, nil
}

func checkManagedFileActionProfile(runtime *managedSession, action Action) error {
	if runtime.ProfileMode == config.BrowserProfileManagedEphemeral ||
		runtime.ProfileMode == config.BrowserProfileManagedPersistent {
		return nil
	}
	return &Error{
		Code: ErrorUnavailable, Operation: action,
		Err: errors.New("browser file transfer requires a managed browser profile"),
	}
}

func (s *Service) AcceptDialog(ctx context.Context, request ActionRequest) (Tab, error) {
	return s.respondToDialog(ctx, ActionAcceptDialog, request, true)
}

func (s *Service) DismissDialog(ctx context.Context, request ActionRequest) (Tab, error) {
	return s.respondToDialog(ctx, ActionDismissDialog, request, false)
}

func (s *Service) ReadArtifact(ctx context.Context, handle string) (ArtifactContent, error) {
	owner, err := ownerFromContext(ctx)
	if err != nil {
		return ArtifactContent{}, err
	}
	artifact, err := s.artifacts.metadata(handle, owner)
	if err != nil {
		return ArtifactContent{}, err
	}
	operation, err := getArtifactReadOperation(owner, artifact)
	if err != nil {
		return ArtifactContent{}, err
	}
	if err := s.checkOperations(ctx, []permissions.Operation{operation}); err != nil {
		return ArtifactContent{}, err
	}
	return s.artifacts.read(handle, owner)
}

func (s *Service) ExportArtifact(ctx context.Context, request ArtifactExportRequest) (Artifact, error) {
	owner, err := ownerFromContext(ctx)
	if err != nil {
		return Artifact{}, err
	}
	artifact, err := s.artifacts.metadata(request.Handle, owner)
	if err != nil {
		return Artifact{}, err
	}
	operations, err := getArtifactExportOperations(owner, artifact, request)
	if err != nil {
		return Artifact{}, err
	}
	if err := s.checkOperations(ctx, operations); err != nil {
		return Artifact{}, err
	}
	content, err := s.artifacts.read(request.Handle, owner)
	if err != nil {
		return Artifact{}, err
	}
	if err := WriteArtifactExport(request.Path, content.Data); err != nil {
		return Artifact{}, err
	}
	return content.Artifact, nil
}

func (s *Service) getArtifactExportOperations(
	ctx context.Context,
	request ArtifactExportRequest,
) ([]permissions.Operation, error) {
	owner, err := ownerFromContext(ctx)
	if err != nil {
		return nil, err
	}
	artifact, err := s.artifacts.metadata(request.Handle, owner)
	if err != nil {
		return nil, err
	}
	return getArtifactExportOperations(owner, artifact, request)
}

func getArtifactExportOperations(
	owner Owner,
	artifact Artifact,
	request ArtifactExportRequest,
) ([]permissions.Operation, error) {
	if err := checkCanonicalFileTarget(request.Path, request.FileTarget); err != nil {
		return nil, errors.New("browser artifact export target is invalid")
	}
	readOperation, err := getArtifactReadOperation(owner, artifact)
	if err != nil {
		return nil, err
	}
	fileOperation, err := (permissions.Operation{
		Tool: "browser", Resource: permissions.ResourceFile, Action: permissions.ActionCreate,
		Effects: append([]permissions.Effect{permissions.EffectWrite}, artifact.Effects...),
		Target:  request.FileTarget, TargetScope: request.TargetScope, OwnerID: owner.Actor.ID,
	}).Normalize()
	if err != nil {
		return nil, err
	}
	return []permissions.Operation{readOperation, fileOperation}, nil
}

func getArtifactReadOperation(owner Owner, artifact Artifact) (permissions.Operation, error) {
	return (permissions.Operation{
		Tool: "browser", Resource: permissions.ResourceBrowser, Action: permissions.ActionRead,
		Effects: append([]permissions.Effect{permissions.EffectRead}, artifact.Effects...),
		Target:  "artifact:" + artifact.Handle, OwnerID: owner.Actor.ID,
	}).Normalize()
}

func WriteArtifactExport(path string, data []byte) error {
	path = filepath.Clean(path)
	parent := filepath.Dir(path)
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return err
	}
	if !isSamePath(resolvedParent, parent) {
		return errors.New("browser artifact export path must not traverse a symbolic link or junction")
	}
	temporary, err := os.CreateTemp(parent, ".morph-browser-export-*.part")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	remove := true
	defer func() {
		_ = temporary.Close()
		if remove {
			_ = removeArtifactExportTemporary(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if err := writeArtifactExportContent(temporary, data); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := publishArtifactExport(temporaryPath, path); err != nil {
		if !checkArtifactExportLinkUnsupported(err) {
			return err
		}
		if err := writeArtifactExportExclusive(path, data); err != nil {
			return err
		}
		if removeArtifactExportTemporary(temporaryPath) == nil {
			remove = false
		}
		return nil
	}
	if removeArtifactExportTemporary(temporaryPath) == nil {
		remove = false
	}
	return nil
}

func writeArtifactExportExclusive(path string, data []byte) error {
	destination, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	remove := true
	defer func() {
		_ = destination.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	if err := writeArtifactExportContent(destination, data); err != nil {
		return err
	}
	if err := destination.Close(); err != nil {
		return err
	}
	remove = false
	return nil
}

func writeAndSyncArtifactExportContent(file *os.File, data []byte) error {
	if _, err := file.Write(data); err != nil {
		return err
	}
	return file.Sync()
}

func ResolveArtifactExportPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(absolute)), nil
}

func isSamePath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func checkCanonicalFileTarget(path, target string) error {
	if !filepath.IsAbs(path) || strings.TrimSpace(target) == "" {
		return errors.New("browser file target is invalid")
	}
	path = filepath.Clean(path)
	targetPath := filepath.Clean(filepath.FromSlash(target))
	if !filepath.IsAbs(targetPath) || !isSamePath(path, targetPath) {
		return errors.New("browser file target does not match the filesystem path")
	}
	return nil
}

func (s *Service) getRichRuntime(
	ctx context.Context,
	sessionID string,
	action Action,
) (*managedSession, InteractiveBackendSession, RichBackendSession, error) {
	runtime, interactive, err := s.getInteractiveRuntime(ctx, sessionID, action)
	if err != nil {
		return nil, nil, nil, err
	}
	rich, ok := runtime.backend.(RichBackendSession)
	if !ok {
		return nil, nil, nil, &Error{
			Code: ErrorUnavailable, Operation: action, Err: errors.New("browser backend does not support rich actions"),
		}
	}
	return runtime, interactive, rich, nil
}

func (s *Service) captureArtifact(
	ctx context.Context,
	action Action,
	request ActionRequest,
	capture func(context.Context, RichBackendSession, string) (BackendArtifact, error),
) (Artifact, error) {
	runtime, _, backend, err := s.getRichRuntime(ctx, request.SessionID, action)
	if err != nil {
		return Artifact{}, err
	}
	runtime.actionMu.Lock()
	defer runtime.actionMu.Unlock()
	operations, err := s.resolveOperations(ctx, action, request, false)
	if err != nil {
		return Artifact{}, err
	}
	if err := s.checkOperations(ctx, operations); err != nil {
		return Artifact{}, err
	}
	tab, err := s.getTab(ctx, runtime, request.TabID)
	if err != nil {
		return Artifact{}, err
	}
	value, err := capture(ctx, backend, tab.ID)
	if err != nil {
		return Artifact{}, getActionError(action, err)
	}
	expectedKind := ArtifactScreenshot
	if action == ActionPDF {
		expectedKind = ArtifactPDF
	}
	if value.Kind != expectedKind {
		return Artifact{}, errors.New("browser backend returned the wrong artifact kind")
	}
	artifact, err := s.storeArtifact(runtime, tab, operations, value)
	if err != nil {
		return Artifact{}, err
	}
	s.touchRuntime(runtime)
	return artifact, nil
}

func (s *Service) storeArtifact(
	runtime *managedSession,
	tab *managedTab,
	operations []permissions.Operation,
	value BackendArtifact,
) (Artifact, error) {
	effects := make([]permissions.Effect, 0)
	for _, operation := range operations {
		effects = append(effects, operation.Effects...)
	}
	if tab.sensitive || runtime.ProfileMode == config.BrowserProfileManagedPersistent ||
		runtime.ProfileMode == config.BrowserProfileExistingSession {
		effects = append(effects, permissions.EffectCredentialBearing)
	}
	source := tab.URL
	if value.SourceURL != "" {
		source = value.SourceURL
	}
	return s.artifacts.create(runtime.Owner, runtime.Profile, source, effects, value)
}

func (s *Service) respondToDialog(
	ctx context.Context,
	action Action,
	request ActionRequest,
	accept bool,
) (Tab, error) {
	runtime, interactive, backend, err := s.getRichRuntime(ctx, request.SessionID, action)
	if err != nil {
		return Tab{}, err
	}
	runtime.actionMu.Lock()
	defer runtime.actionMu.Unlock()
	if err := s.authorizeAction(ctx, action, request, false); err != nil {
		return Tab{}, err
	}
	tab, err := s.getTab(ctx, runtime, request.TabID)
	if err != nil {
		return Tab{}, err
	}
	nodeID, err := getReference(tab, request.Ref)
	if err != nil {
		return Tab{}, err
	}
	actionCtx, finishAction, err := s.prepareNetworkAction(ctx, runtime, interactive, action, tab.ID)
	if err != nil {
		return Tab{}, getActionError(action, err)
	}
	defer finishAction()
	if err := backend.RespondToDialog(actionCtx, tab.ID, nodeID, accept, request.Text); err != nil {
		return Tab{}, getActionError(action, err)
	}
	if err := waitForNetworkSettlement(actionCtx, interactive, tab.ID); err != nil {
		return Tab{}, getActionError(action, err)
	}
	s.bumpTabGeneration(runtime, tab.ID)
	s.touchRuntime(runtime)
	return s.getTabCopy(runtime, tab.ID), nil
}
