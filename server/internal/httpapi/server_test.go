package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/diffflow/server/internal/auth"
	"github.com/diffflow/server/internal/files"
	"github.com/diffflow/server/internal/hub"
	"github.com/diffflow/server/internal/store"
)

func TestLoginProjectAndFileSync(t *testing.T) {
	tmp := t.TempDir()
	db, err := store.NewDB(filepath.Join(tmp, "diffflow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	adminHash, err := auth.HashPassword("admin-pass")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.EnsureConfiguredAdmin("admin", adminHash); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureSetting(maxFileSettingKey, strconv.FormatInt(100*1024*1024, 10)); err != nil {
		t.Fatal(err)
	}
	project, err := db.CreateProject("Game")
	if err != nil {
		t.Fatal(err)
	}
	userHash, err := auth.HashPassword("alice-pass")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateUser("alice", userHash, []int64{project.ID}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateInvite("invite-key", 1, 0, []int64{project.ID}); err != nil {
		t.Fatal(err)
	}

	objectStore, err := files.NewStore(filepath.Join(tmp, "files"))
	if err != nil {
		t.Fatal(err)
	}
	app := New(db, auth.NewTokenManager("test-secret"), hub.NewBroker(), objectStore, 100*1024*1024)
	server := httptest.NewServer(app.Routes())
	defer server.Close()

	if token := registerForTest(t, server.URL, "bob", "bob-pass", "invite-key"); token == "" {
		t.Fatal("empty register token")
	}
	token := loginForTest(t, server.URL, "alice", "alice-pass")
	req, err := http.NewRequest(http.MethodGet, server.URL+"/api/projects", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("projects status = %d", resp.StatusCode)
	}

	fileBaseURL := server.URL + "/api/projects/" + strconv.FormatInt(project.ID, 10) + "/files?path=scenes/main.tscn"
	fileURL := fileBaseURL + "&mtime=123&base_sha="
	req, err = http.NewRequest(http.MethodPut, fileURL, bytes.NewBufferString("scene-data"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-DiffFlow-Peer", "peer-a")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("upload status = %d body = %s", resp.StatusCode, string(body))
	}
	resp.Body.Close()

	req, err = http.NewRequest(http.MethodGet, server.URL+"/api/projects/"+strconv.FormatInt(project.ID, 10)+"/manifest", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Files []store.FileSnapshot `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(manifest.Files) != 1 || manifest.Files[0].Path != "scenes/main.tscn" {
		t.Fatalf("unexpected manifest: %#v", manifest.Files)
	}

	req, err = http.NewRequest(http.MethodGet, server.URL+"/api/projects/"+strconv.FormatInt(project.ID, 10)+"/files?path=scenes/main.tscn", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "scene-data" {
		t.Fatalf("downloaded %q", string(body))
	}

	baseSHA := manifest.Files[0].SHA256
	req, err = http.NewRequest(http.MethodDelete, fileBaseURL+"&mtime="+strconv.FormatInt(time.Now().Unix(), 10)+"&base_sha="+baseSHA, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d", resp.StatusCode)
	}
	files, err := db.ListManifest(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("manifest after delete = %#v", files)
	}
}

func TestFileUploadRejectsStaleBaseSHA(t *testing.T) {
	tmp := t.TempDir()
	db, err := store.NewDB(filepath.Join(tmp, "diffflow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	adminHash, err := auth.HashPassword("admin-pass")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.EnsureConfiguredAdmin("admin", adminHash); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureSetting(maxFileSettingKey, strconv.FormatInt(100*1024*1024, 10)); err != nil {
		t.Fatal(err)
	}
	project, err := db.CreateProject("Game")
	if err != nil {
		t.Fatal(err)
	}
	userHash, err := auth.HashPassword("alice-pass")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateUser("alice", userHash, []int64{project.ID}); err != nil {
		t.Fatal(err)
	}

	objectStore, err := files.NewStore(filepath.Join(tmp, "files"))
	if err != nil {
		t.Fatal(err)
	}
	app := New(db, auth.NewTokenManager("test-secret"), hub.NewBroker(), objectStore, 100*1024*1024)
	server := httptest.NewServer(app.Routes())
	defer server.Close()

	token := loginForTest(t, server.URL, "alice", "alice-pass")
	fileBaseURL := server.URL + "/api/projects/" + strconv.FormatInt(project.ID, 10) + "/files?path=scenes/main.tscn"

	req, err := http.NewRequest(http.MethodPut, fileBaseURL+"&mtime=100&base_sha=", bytes.NewBufferString("v1"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initial upload status = %d", resp.StatusCode)
	}

	snapshot, err := db.GetFileSnapshot(project.ID, "scenes/main.tscn")
	if err != nil {
		t.Fatal(err)
	}

	req, err = http.NewRequest(http.MethodPut, fileBaseURL+"&mtime=101&base_sha=", bytes.NewBufferString("stale"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("stale upload status = %d body = %s", resp.StatusCode, string(body))
	}

	var conflict struct {
		CurrentSHA string `json:"current_sha"`
	}
	if err := json.Unmarshal(body, &conflict); err != nil {
		t.Fatal(err)
	}
	if conflict.CurrentSHA != snapshot.SHA256 {
		t.Fatalf("current_sha = %q, want %q", conflict.CurrentSHA, snapshot.SHA256)
	}

	req, err = http.NewRequest(http.MethodPut, fileBaseURL+"&mtime=102&base_sha="+snapshot.SHA256, bytes.NewBufferString("v2"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("overwrite upload status = %d", resp.StatusCode)
	}
}

func registerForTest(t *testing.T, baseURL, username, password, inviteKey string) string {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"username":   username,
		"password":   password,
		"invite_key": inviteKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(baseURL+"/api/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		responseBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("register status = %d body = %s", resp.StatusCode, string(responseBody))
	}
	var result struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result.Token
}

func loginForTest(t *testing.T, baseURL, username, password string) string {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(baseURL+"/api/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("login status = %d body = %s", resp.StatusCode, string(responseBody))
	}
	var result struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Token == "" {
		t.Fatal("empty login token")
	}
	return result.Token
}
