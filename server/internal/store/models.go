package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")

type User struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
	IsAdmin      bool   `json:"is_admin"`
	Enabled      bool   `json:"enabled"`
}

type UserWithProjects struct {
	User
	Projects []Project `json:"projects"`
}

type Project struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type Invite struct {
	Key       string    `json:"key"`
	MaxUses   int       `json:"max_uses"`
	Uses      int       `json:"uses"`
	ExpiresAt int64     `json:"expires_at"`
	Projects  []Project `json:"projects"`
}

type FileSnapshot struct {
	ProjectID int64  `json:"project_id"`
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	MTime     int64  `json:"mtime"`
	UpdatedBy int64  `json:"updated_by"`
	UpdatedAt int64  `json:"updated_at"`
	Deleted   bool   `json:"deleted"`
}

func (d *DB) EnsureConfiguredAdmin(username, passwordHash string) (*User, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	tx, err := d.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("UPDATE users SET is_admin = 0 WHERE is_admin = 1 AND username <> ?", username); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(
		`INSERT INTO users (username, password_hash, is_admin, enabled)
		 VALUES (?, ?, 1, 1)
		 ON CONFLICT(username) DO UPDATE SET
			password_hash = excluded.password_hash,
			is_admin = 1,
			enabled = 1`,
		username, passwordHash,
	); err != nil {
		return nil, err
	}

	user, err := scanUser(tx.QueryRow("SELECT id, username, password_hash, is_admin, enabled FROM users WHERE username = ?", username))
	if err != nil {
		return nil, err
	}
	return user, tx.Commit()
}

func (d *DB) EnsureSetting(key, value string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec("INSERT OR IGNORE INTO settings (key, value) VALUES (?, ?)", key, value)
	return err
}

func (d *DB) SetSetting(key, value string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	return err
}

func (d *DB) GetSettingInt64(key string, fallback int64) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var value string
	err := d.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return fallback, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("setting %s is not an integer: %w", key, err)
	}
	return n, nil
}

func (d *DB) GetUserByUsername(username string) (*User, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	user, err := scanUser(d.db.QueryRow("SELECT id, username, password_hash, is_admin, enabled FROM users WHERE username = ?", username))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return user, err
}

func (d *DB) GetUserByID(id int64) (*User, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	user, err := scanUser(d.db.QueryRow("SELECT id, username, password_hash, is_admin, enabled FROM users WHERE id = ?", id))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return user, err
}

func (d *DB) ListUsers() ([]UserWithProjects, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.db.Query("SELECT id, username, password_hash, is_admin, enabled FROM users ORDER BY is_admin DESC, username")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []UserWithProjects
	for rows.Next() {
		var user User
		var isAdmin, enabled int
		if err := rows.Scan(&user.ID, &user.Username, &user.PasswordHash, &isAdmin, &enabled); err != nil {
			return nil, err
		}
		user.IsAdmin = isAdmin != 0
		user.Enabled = enabled != 0
		users = append(users, UserWithProjects{User: user})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range users {
		projects, err := listProjectsForUser(d.db, users[i].ID, users[i].IsAdmin)
		if err != nil {
			return nil, err
		}
		users[i].Projects = projects
	}
	return users, nil
}

func (d *DB) CreateUser(username, passwordHash string, projectIDs []int64) (*User, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	tx, err := d.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		"INSERT INTO users (username, password_hash, is_admin, enabled) VALUES (?, ?, 0, 1)",
		username, passwordHash,
	)
	if err != nil {
		return nil, err
	}
	userID, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	if err := replaceUserProjects(tx, userID, projectIDs); err != nil {
		return nil, err
	}
	user, err := scanUser(tx.QueryRow("SELECT id, username, password_hash, is_admin, enabled FROM users WHERE id = ?", userID))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return user, nil
}

func (d *DB) SetUserPassword(userID int64, passwordHash string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec("UPDATE users SET password_hash = ? WHERE id = ? AND is_admin = 0", passwordHash, userID)
	return err
}

func (d *DB) SetUserEnabled(userID int64, enabled bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec("UPDATE users SET enabled = ? WHERE id = ? AND is_admin = 0", boolInt(enabled), userID)
	return err
}

func (d *DB) UpdateUserProjects(userID int64, projectIDs []int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := replaceUserProjects(tx, userID, projectIDs); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) CreateProject(name string) (*Project, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.db.Exec("INSERT INTO projects (name) VALUES (?)", name)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &Project{ID: id, Name: name}, nil
}

func (d *DB) ListProjects() ([]Project, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return listProjects(d.db)
}

func (d *DB) ListProjectsForUser(userID int64, isAdmin bool) ([]Project, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return listProjectsForUser(d.db, userID, isAdmin)
}

func (d *DB) CanAccessProject(userID, projectID int64, isAdmin bool) (bool, error) {
	if isAdmin {
		return true, nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	var one int
	err := d.db.QueryRow("SELECT 1 FROM project_members WHERE user_id = ? AND project_id = ?", userID, projectID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (d *DB) CreateInvite(key string, maxUses int, expiresAt int64, projectIDs []int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		"INSERT INTO invite_keys (key, max_uses, uses, expires_at) VALUES (?, ?, 0, ?)",
		key, maxUses, expiresAt,
	); err != nil {
		return err
	}
	for _, projectID := range projectIDs {
		if _, err := tx.Exec("INSERT INTO invite_projects (key, project_id) VALUES (?, ?)", key, projectID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) ListInvites() ([]Invite, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.db.Query("SELECT key, max_uses, uses, expires_at FROM invite_keys ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var invites []Invite
	for rows.Next() {
		var invite Invite
		if err := rows.Scan(&invite.Key, &invite.MaxUses, &invite.Uses, &invite.ExpiresAt); err != nil {
			return nil, err
		}
		invite.Projects, err = listProjectsForInvite(d.db, invite.Key)
		if err != nil {
			return nil, err
		}
		invites = append(invites, invite)
	}
	return invites, rows.Err()
}

func (d *DB) ConsumeInvite(key, username, passwordHash string) (*User, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	tx, err := d.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var maxUses, uses int
	var expiresAt int64
	err = tx.QueryRow("SELECT max_uses, uses, expires_at FROM invite_keys WHERE key = ?", key).Scan(&maxUses, &uses, &expiresAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if maxUses > 0 && uses >= maxUses {
		return nil, errors.New("invite key has been used up")
	}
	if expiresAt > 0 && expiresAt < time.Now().Unix() {
		return nil, errors.New("invite key has expired")
	}

	res, err := tx.Exec(
		"INSERT INTO users (username, password_hash, is_admin, enabled) VALUES (?, ?, 0, 1)",
		username, passwordHash,
	)
	if err != nil {
		return nil, err
	}
	userID, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	projectIDs, err := inviteProjectIDs(tx, key)
	if err != nil {
		return nil, err
	}
	if err := replaceUserProjects(tx, userID, projectIDs); err != nil {
		return nil, err
	}
	if _, err := tx.Exec("UPDATE invite_keys SET uses = uses + 1 WHERE key = ?", key); err != nil {
		return nil, err
	}
	user, err := scanUser(tx.QueryRow("SELECT id, username, password_hash, is_admin, enabled FROM users WHERE id = ?", userID))
	if err != nil {
		return nil, err
	}
	return user, tx.Commit()
}

func (d *DB) UpsertFileObject(sha256 string, size int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec(
		`INSERT INTO file_objects (sha256, size) VALUES (?, ?)
		 ON CONFLICT(sha256) DO NOTHING`,
		sha256, size,
	)
	return err
}

func (d *DB) UpsertFileSnapshot(projectID int64, path, sha256 string, size, mtime, userID int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec(
		`INSERT INTO file_snapshots (project_id, path, sha256, size, mtime, updated_by, deleted)
		 VALUES (?, ?, ?, ?, ?, ?, 0)
		 ON CONFLICT(project_id, path) DO UPDATE SET
			sha256 = excluded.sha256,
			size = excluded.size,
			mtime = excluded.mtime,
			updated_by = excluded.updated_by,
			updated_at = strftime('%s','now'),
			deleted = 0`,
		projectID, path, sha256, size, mtime, userID,
	)
	return err
}

func (d *DB) UpsertFileSnapshotIfBase(projectID int64, path, sha256 string, size, mtime, userID int64, baseSHA string) (*FileSnapshot, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	tx, err := d.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	current, err := fileSnapshotInTx(tx, projectID, path)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	currentSHA := ""
	if current != nil && !current.Deleted {
		currentSHA = current.SHA256
	}
	if currentSHA != baseSHA {
		return current, ErrConflict
	}

	_, err = tx.Exec(
		`INSERT INTO file_snapshots (project_id, path, sha256, size, mtime, updated_by, deleted)
		 VALUES (?, ?, ?, ?, ?, ?, 0)
		 ON CONFLICT(project_id, path) DO UPDATE SET
			sha256 = excluded.sha256,
			size = excluded.size,
			mtime = excluded.mtime,
			updated_by = excluded.updated_by,
			updated_at = strftime('%s','now'),
			deleted = 0`,
		projectID, path, sha256, size, mtime, userID,
	)
	if err != nil {
		return nil, err
	}
	return nil, tx.Commit()
}

func (d *DB) MarkFileDeleted(projectID int64, path string, mtime, userID int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec(
		`INSERT INTO file_snapshots (project_id, path, sha256, size, mtime, updated_by, deleted)
		 VALUES (?, ?, '', 0, ?, ?, 1)
		 ON CONFLICT(project_id, path) DO UPDATE SET
			size = 0,
			mtime = excluded.mtime,
			updated_by = excluded.updated_by,
			updated_at = strftime('%s','now'),
			deleted = 1`,
		projectID, path, mtime, userID,
	)
	return err
}

func (d *DB) MarkFileDeletedIfBase(projectID int64, path string, mtime, userID int64, baseSHA string) (*FileSnapshot, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	tx, err := d.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	current, err := fileSnapshotInTx(tx, projectID, path)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	currentSHA := ""
	if current != nil && !current.Deleted {
		currentSHA = current.SHA256
	}
	if currentSHA != baseSHA {
		return current, ErrConflict
	}

	_, err = tx.Exec(
		`INSERT INTO file_snapshots (project_id, path, sha256, size, mtime, updated_by, deleted)
		 VALUES (?, ?, '', 0, ?, ?, 1)
		 ON CONFLICT(project_id, path) DO UPDATE SET
			size = 0,
			mtime = excluded.mtime,
			updated_by = excluded.updated_by,
			updated_at = strftime('%s','now'),
			deleted = 1`,
		projectID, path, mtime, userID,
	)
	if err != nil {
		return nil, err
	}
	return nil, tx.Commit()
}

func (d *DB) GetFileSnapshot(projectID int64, path string) (*FileSnapshot, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	snapshot, err := fileSnapshotInTx(d.db, projectID, path)
	return snapshot, err
}

func fileSnapshotInTx(db interface {
	QueryRow(query string, args ...any) *sql.Row
}, projectID int64, path string) (*FileSnapshot, error) {
	snapshot, err := scanFileSnapshot(db.QueryRow(
		`SELECT project_id, path, sha256, size, mtime, updated_by, updated_at, deleted
		 FROM file_snapshots WHERE project_id = ? AND path = ?`,
		projectID, path,
	))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return snapshot, err
}

func (d *DB) ListManifest(projectID int64) ([]FileSnapshot, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rows, err := d.db.Query(
		`SELECT project_id, path, sha256, size, mtime, updated_by, updated_at, deleted
		 FROM file_snapshots
		 WHERE project_id = ? AND deleted = 0
		 ORDER BY path`,
		projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []FileSnapshot
	for rows.Next() {
		snapshot, err := scanFileSnapshot(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *snapshot)
	}
	return result, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(row rowScanner) (*User, error) {
	var user User
	var isAdmin, enabled int
	if err := row.Scan(&user.ID, &user.Username, &user.PasswordHash, &isAdmin, &enabled); err != nil {
		return nil, err
	}
	user.IsAdmin = isAdmin != 0
	user.Enabled = enabled != 0
	return &user, nil
}

func scanFileSnapshot(row rowScanner) (*FileSnapshot, error) {
	var snapshot FileSnapshot
	var deleted int
	if err := row.Scan(
		&snapshot.ProjectID,
		&snapshot.Path,
		&snapshot.SHA256,
		&snapshot.Size,
		&snapshot.MTime,
		&snapshot.UpdatedBy,
		&snapshot.UpdatedAt,
		&deleted,
	); err != nil {
		return nil, err
	}
	snapshot.Deleted = deleted != 0
	return &snapshot, nil
}

func replaceUserProjects(tx *sql.Tx, userID int64, projectIDs []int64) error {
	if _, err := tx.Exec("DELETE FROM project_members WHERE user_id = ?", userID); err != nil {
		return err
	}
	for _, projectID := range projectIDs {
		if _, err := tx.Exec("INSERT OR IGNORE INTO project_members (user_id, project_id) VALUES (?, ?)", userID, projectID); err != nil {
			return err
		}
	}
	return nil
}

func inviteProjectIDs(tx *sql.Tx, key string) ([]int64, error) {
	rows, err := tx.Query("SELECT project_id FROM invite_projects WHERE key = ?", key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func listProjects(db *sql.DB) ([]Project, error) {
	rows, err := db.Query("SELECT id, name FROM projects ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var projects []Project
	for rows.Next() {
		var project Project
		if err := rows.Scan(&project.ID, &project.Name); err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

func listProjectsForUser(db *sql.DB, userID int64, isAdmin bool) ([]Project, error) {
	if isAdmin {
		return listProjects(db)
	}
	rows, err := db.Query(
		`SELECT p.id, p.name
		 FROM projects p
		 INNER JOIN project_members pm ON pm.project_id = p.id
		 WHERE pm.user_id = ?
		 ORDER BY p.name`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var projects []Project
	for rows.Next() {
		var project Project
		if err := rows.Scan(&project.ID, &project.Name); err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

func listProjectsForInvite(db *sql.DB, key string) ([]Project, error) {
	rows, err := db.Query(
		`SELECT p.id, p.name
		 FROM projects p
		 INNER JOIN invite_projects ip ON ip.project_id = p.id
		 WHERE ip.key = ?
		 ORDER BY p.name`,
		key,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var projects []Project
	for rows.Next() {
		var project Project
		if err := rows.Scan(&project.ID, &project.Name); err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
