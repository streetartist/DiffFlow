package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/diffflow/server/internal/auth"
	"github.com/diffflow/server/internal/files"
	"github.com/diffflow/server/internal/hub"
	"github.com/diffflow/server/internal/store"
	"github.com/diffflow/server/internal/ws"
)

const maxFileSettingKey = "sync.max_file_bytes"

func MaxFileSettingKey() string {
	return maxFileSettingKey
}

type Server struct {
	db      *store.DB
	tokens  *auth.TokenManager
	broker  *hub.Broker
	files   *files.Store
	baseMax int64
}

func New(db *store.DB, tokens *auth.TokenManager, broker *hub.Broker, fileStore *files.Store, baseMax int64) *Server {
	return &Server{
		db:      db,
		tokens:  tokens,
		broker:  broker,
		files:   fileStore,
		baseMax: baseMax,
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		ws.HandleWebSocket(s.broker, s.db, s.tokens, w, r)
	})

	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/register", s.handleRegister)
	mux.HandleFunc("/api/projects", s.handleProjects)
	mux.HandleFunc("/api/projects/", s.handleProjectAPI)

	mux.HandleFunc("/admin/login", s.handleAdminLogin)
	mux.HandleFunc("/admin/logout", s.handleAdminLogout)
	mux.HandleFunc("/admin", s.handleAdminDashboard)
	mux.HandleFunc("/admin/", s.handleAdminAction)
	return mux
}

func (s *Server) maxFileBytes() (int64, error) {
	return s.db.GetSettingInt64(maxFileSettingKey, s.baseMax)
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusFound)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) requireUser(w http.ResponseWriter, r *http.Request) (*store.User, bool) {
	token := bearerToken(r)
	if token == "" {
		if cookie, err := r.Cookie("df_token"); err == nil {
			token = cookie.Value
		}
	}
	if token == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return nil, false
	}
	claims, err := s.tokens.Parse(token)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return nil, false
	}
	user, err := s.db.GetUserByID(claims.UserID)
	if err != nil || !user.Enabled {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return nil, false
	}
	return user, true
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (*store.User, bool) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return nil, false
	}
	if !user.IsAdmin {
		writeError(w, http.StatusForbidden, "admin required")
		return nil, false
	}
	return user, true
}

func (s *Server) requireProject(w http.ResponseWriter, r *http.Request, projectID int64) (*store.User, bool) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return nil, false
	}
	allowed, err := s.db.CanAccessProject(user.ID, projectID, user.IsAdmin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project lookup failed")
		return nil, false
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "project access denied")
		return nil, false
	}
	return user, true
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if strings.HasPrefix(header, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	}
	return ""
}

func parseProjectAPIPath(path string) (int64, string, bool) {
	rest := strings.TrimPrefix(path, "/api/projects/")
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		return 0, "", false
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		return 0, "", false
	}
	return id, parts[1], true
}

func normalizeRelPath(path string) (string, error) {
	path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return "", errors.New("path is required")
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return "", errors.New("invalid path")
		}
	}
	if strings.Contains(path, ":") {
		return "", errors.New("invalid path")
	}
	return path, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if value != nil {
		_ = json.NewEncoder(w).Encode(value)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func decodeJSON(r *http.Request, dst any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(dst)
}

func issueLoginToken(tokens *auth.TokenManager, user *store.User) (string, error) {
	return tokens.Issue(user.ID, user.Username, user.IsAdmin, 24*time.Hour)
}

func parseInt64Form(r *http.Request, name string, fallback int64) int64 {
	value := strings.TrimSpace(r.FormValue(name))
	if value == "" {
		return fallback
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

func selectedProjectIDs(r *http.Request) []int64 {
	values := r.Form["project_ids"]
	ids := make([]int64, 0, len(values))
	for _, value := range values {
		id, err := strconv.ParseInt(value, 10, 64)
		if err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

func humanMB(bytes int64) int64 {
	if bytes <= 0 {
		return 0
	}
	return bytes / 1024 / 1024
}

func parseExpires(days int64) int64 {
	if days <= 0 {
		return 0
	}
	return time.Now().Add(time.Duration(days) * 24 * time.Hour).Unix()
}

func projectIDSet(projects []store.Project) map[int64]bool {
	set := make(map[int64]bool, len(projects))
	for _, project := range projects {
		set[project.ID] = true
	}
	return set
}

func formError(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%v", err)
}
