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

	fileURL := server.URL + "/api/projects/" + strconv.FormatInt(project.ID, 10) + "/files?path=scenes/main.tscn&mtime=123"
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

	req, err = http.NewRequest(http.MethodDelete, fileURL+"&mtime="+strconv.FormatInt(time.Now().Unix(), 10), nil)
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
