package inspect

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"strings"

	"github.com/xymorphic/morph/pkg/str"
)

//go:embed assets/*
var assets embed.FS

var readAssetFile = fs.ReadFile

// App serves trace-inspection pages and JSON endpoints.
type App struct {
	store          *Store
	memoryProvider SessionMemoryProvider
	username       string
	password       string
}

// NewApp returns the HTTP application used by trace inspection.
func NewApp(directory string) *App {
	return &App{store: NewStore(directory)}
}

func (a *App) SetMemoryProvider(provider SessionMemoryProvider) {
	if a == nil {
		return
	}

	a.memoryProvider = provider
}

func (a *App) SetBasicAuth(username, password string) {
	if a == nil {
		return
	}

	usernameValue := str.String(username)
	a.username = usernameValue.Trim()
	a.password = password
}

func (a *App) Validate() error {
	if a == nil || a.store == nil {
		return errors.New("trace app is required")
	}

	return a.store.Validate()
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/sessions", a.handleSessions)
	mux.HandleFunc("/api/sessions/", a.handleSession)
	mux.Handle("/assets/", http.FileServer(http.FS(assetFS())))
	mux.HandleFunc("/", a.handleIndex)
	if !a.requiresAuth() {
		return mux
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.authorized(r) {
			mux.ServeHTTP(w, r)
			return
		}

		w.Header().Set("WWW-Authenticate", `Basic realm="Morph Trace Viewer", charset="UTF-8"`)
		writeError(w, http.StatusUnauthorized, "authentication required")
	})
}

func (a *App) requiresAuth() bool {
	if a == nil {
		return false
	}

	return a.username != "" || a.password != ""
}

func (a *App) authorized(r *http.Request) bool {
	if a == nil || !a.requiresAuth() {
		return true
	}

	username, password, ok := r.BasicAuth()
	if !ok {
		return false
	}

	if subtle.ConstantTimeCompare([]byte(username), []byte(a.username)) != 1 {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(password), []byte(a.password)) == 1
}

func (a *App) handleIndex(w http.ResponseWriter, _ *http.Request) {
	content, err := readAssetFile(assets, "assets/index.html")
	if err != nil {
		http.Error(w, "failed to load trace viewer", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(content)
}

func assetFS() fs.FS {
	sub, _ := fs.Sub(assets, "assets")
	return sub
}

func (a *App) handleSessions(w http.ResponseWriter, _ *http.Request) {
	sessions, err := a.store.ListSessions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

func (a *App) handleSession(w http.ResponseWriter, r *http.Request) {
	trimPrefixValue := str.String(strings.TrimPrefix(r.URL.Path, "/api/sessions/"))
	id := trimPrefixValue.Trim()
	if id == "" {
		http.NotFound(w, r)
		return
	}

	detail, err := a.store.GetSession(id)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		if errors.Is(err, fs.ErrPermission) {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}

		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.attachMemories(r.Context(), id, &detail)

	writeJSON(w, http.StatusOK, detail)
}

func (a *App) attachMemories(ctx context.Context, sessionID string, detail *SessionDetail) {
	if a == nil || a.memoryProvider == nil || detail == nil {
		return
	}

	memories, err := a.loadSessionMemories(ctx, sessionID)
	if err != nil {
		detail.Memories = &SessionMemoryView{
			Source:    "state",
			LoadError: err.Error(),
		}
		return
	}

	detail.Memories = &SessionMemoryView{
		Source: "state",
		Items:  memories,
	}
}

func (a *App) loadSessionMemories(ctx context.Context, sessionID string) ([]MemoryView, error) {
	items, err := a.memoryProvider.ListSessionMemories(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	memories := make([]MemoryView, 0, len(items))
	for _, item := range items {
		memories = append(memories, memoryItemToMemoryView(item))
	}

	return memories, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	messageValue := str.String(message)
	writeJSON(w, status, map[string]any{"error": messageValue.Trim()})
}
