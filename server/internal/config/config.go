package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Server   ServerConfig
	Admin    AdminConfig
	Storage  StorageConfig
	Sync     SyncConfig
	Security SecurityConfig
}

type ServerConfig struct {
	Addr string
}

type AdminConfig struct {
	Username string
	Password string
}

type StorageConfig struct {
	SQLitePath string
	FilesDir   string
}

type SyncConfig struct {
	MaxFileBytes int64
}

type SecurityConfig struct {
	SessionSecret string
}

func Default() *Config {
	return &Config{
		Server: ServerConfig{
			Addr: ":8090",
		},
		Admin: AdminConfig{
			Username: "admin",
			Password: "admin",
		},
		Storage: StorageConfig{
			SQLitePath: "./diffflow.db",
			FilesDir:   "./data/files",
		},
		Sync: SyncConfig{
			MaxFileBytes: 100 * 1024 * 1024,
		},
		Security: SecurityConfig{
			SessionSecret: "change-me",
		},
	}
}

func Load(path string) (*Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}

	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	section := ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = trimValue(value)

		switch section + "." + key {
		case "server.addr":
			cfg.Server.Addr = value
		case "admin.username":
			cfg.Admin.Username = value
		case "admin.password":
			cfg.Admin.Password = value
		case "storage.sqlite_path":
			cfg.Storage.SQLitePath = value
		case "storage.files_dir":
			cfg.Storage.FilesDir = value
		case "sync.max_file_mb":
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("parse sync.max_file_mb: %w", err)
			}
			cfg.Sync.MaxFileBytes = n * 1024 * 1024
		case "sync.max_file_bytes":
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("parse sync.max_file_bytes: %w", err)
			}
			cfg.Sync.MaxFileBytes = n
		case "security.session_secret":
			cfg.Security.SessionSecret = value
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if cfg.Admin.Username == "" || cfg.Admin.Password == "" {
		return nil, fmt.Errorf("admin.username and admin.password are required")
	}
	if cfg.Sync.MaxFileBytes <= 0 {
		return nil, fmt.Errorf("sync max file size must be greater than 0")
	}
	return cfg, nil
}

func trimValue(value string) string {
	value = strings.TrimSpace(value)
	if idx := strings.Index(value, "#"); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}
