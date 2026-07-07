package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/diffflow/server/internal/auth"
	"github.com/diffflow/server/internal/files"
	"github.com/diffflow/server/internal/store"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type registerRequest struct {
	Username  string `json:"username"`
	Password  string `json:"password"`
	InviteKey string `json:"invite_key"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	user, err := s.db.GetUserByUsername(req.Username)
	if err != nil || !user.Enabled || !auth.VerifyPassword(user.PasswordHash, req.Password) {
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	token, err := issueLoginToken(s.tokens, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not issue token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token": token,
		"user":  user,
	})
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req registerRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.InviteKey = strings.TrimSpace(req.InviteKey)
	if req.Username == "" || req.Password == "" || req.InviteKey == "" {
		writeError(w, http.StatusBadRequest, "username, password and invite_key are required")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not hash password")
		return
	}
	user, err := s.db.ConsumeInvite(req.InviteKey, req.Username, hash)
	if err != nil {
		writeError(w, http.StatusBadRequest, formError(err))
		return
	}
	token, err := issueLoginToken(s.tokens, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not issue token")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"token": token,
		"user":  user,
	})
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/projects" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	projects, err := s.db.ListProjectsForUser(user.ID, user.IsAdmin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load projects")
		return
	}
	maxFileBytes, err := s.maxFileBytes()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load settings")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"projects":       projects,
		"max_file_bytes": maxFileBytes,
	})
}

func (s *Server) handleProjectAPI(w http.ResponseWriter, r *http.Request) {
	projectID, resource, ok := parseProjectAPIPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch resource {
	case "manifest":
		s.handleManifest(w, r, projectID)
	case "files":
		s.handleProjectFile(w, r, projectID)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request, projectID int64) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if _, ok := s.requireProject(w, r, projectID); !ok {
		return
	}
	manifest, err := s.db.ListManifest(projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load manifest")
		return
	}
	maxFileBytes, err := s.maxFileBytes()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load settings")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"files":          manifest,
		"max_file_bytes": maxFileBytes,
	})
}

func (s *Server) handleProjectFile(w http.ResponseWriter, r *http.Request, projectID int64) {
	switch r.Method {
	case http.MethodGet:
		s.downloadFile(w, r, projectID)
	case http.MethodPut:
		s.uploadFile(w, r, projectID)
	case http.MethodDelete:
		s.deleteFile(w, r, projectID)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) downloadFile(w http.ResponseWriter, r *http.Request, projectID int64) {
	if _, ok := s.requireProject(w, r, projectID); !ok {
		return
	}
	relPath, err := normalizeRelPath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	snapshot, err := s.db.GetFileSnapshot(projectID, relPath)
	if errors.Is(err, store.ErrNotFound) || (snapshot != nil && snapshot.Deleted) {
		writeError(w, http.StatusNotFound, "file not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load file metadata")
		return
	}
	file, err := s.files.Open(snapshot.SHA256)
	if err != nil {
		writeError(w, http.StatusNotFound, "file object not found")
		return
	}
	defer file.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-DiffFlow-SHA256", snapshot.SHA256)
	w.Header().Set("X-DiffFlow-MTime", strconv.FormatInt(snapshot.MTime, 10))
	w.Header().Set("Content-Length", strconv.FormatInt(snapshot.Size, 10))
	_, _ = io.Copy(w, file)
}

func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request, projectID int64) {
	user, ok := s.requireProject(w, r, projectID)
	if !ok {
		return
	}
	relPath, err := normalizeRelPath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	baseSHA, err := requiredBaseSHA(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	maxFileBytes, err := s.maxFileBytes()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load settings")
		return
	}
	sha, size, err := s.files.Save(r.Body, maxFileBytes)
	if errors.Is(err, files.ErrTooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "file exceeds sync threshold")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not save file")
		return
	}
	mtime := parseInt64Query(r, "mtime", time.Now().Unix())
	if err := s.db.UpsertFileObject(sha, size); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save file object")
		return
	}
	if current, err := s.db.UpsertFileSnapshotIfBase(projectID, relPath, sha, size, mtime, user.ID, baseSHA); errors.Is(err, store.ErrConflict) {
		writeSnapshotConflict(w, current)
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not save file snapshot")
		return
	}

	event := map[string]any{
		"type":       "file_updated",
		"project_id": projectID,
		"path":       relPath,
		"sha256":     sha,
		"size":       size,
		"mtime":      mtime,
		"user_id":    user.ID,
		"username":   user.Username,
		"peer_id":    r.Header.Get("X-DiffFlow-Peer"),
	}
	s.broker.Broadcast(projectID, event)
	writeJSON(w, http.StatusOK, event)
}

func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request, projectID int64) {
	user, ok := s.requireProject(w, r, projectID)
	if !ok {
		return
	}
	relPath, err := normalizeRelPath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	baseSHA, err := requiredBaseSHA(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	mtime := parseInt64Query(r, "mtime", time.Now().Unix())
	if current, err := s.db.MarkFileDeletedIfBase(projectID, relPath, mtime, user.ID, baseSHA); errors.Is(err, store.ErrConflict) {
		writeSnapshotConflict(w, current)
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete file")
		return
	}
	event := map[string]any{
		"type":       "file_deleted",
		"project_id": projectID,
		"path":       relPath,
		"mtime":      mtime,
		"user_id":    user.ID,
		"username":   user.Username,
		"peer_id":    r.Header.Get("X-DiffFlow-Peer"),
	}
	s.broker.Broadcast(projectID, event)
	writeJSON(w, http.StatusOK, event)
}

func parseInt64Query(r *http.Request, key string, fallback int64) int64 {
	value := strings.TrimSpace(r.URL.Query().Get(key))
	if value == "" {
		return fallback
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

func requiredBaseSHA(r *http.Request) (string, error) {
	values, ok := r.URL.Query()["base_sha"]
	if !ok {
		return "", errors.New("base_sha is required")
	}
	baseSHA := strings.TrimSpace(values[0])
	if baseSHA == "" {
		return "", nil
	}
	if len(baseSHA) != 64 {
		return "", errors.New("base_sha must be empty or a sha256 hex string")
	}
	for _, ch := range baseSHA {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
			return "", errors.New("base_sha must be empty or a sha256 hex string")
		}
	}
	return strings.ToLower(baseSHA), nil
}

func writeSnapshotConflict(w http.ResponseWriter, current *store.FileSnapshot) {
	currentSHA := ""
	if current != nil && !current.Deleted {
		currentSHA = current.SHA256
	}
	writeJSON(w, http.StatusConflict, map[string]any{
		"error":       "conflict",
		"current_sha": currentSHA,
		"current":     current,
	})
}
