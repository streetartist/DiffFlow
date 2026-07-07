package main

import (
	"flag"
	"log"
	"net/http"
	"strconv"

	"github.com/diffflow/server/internal/auth"
	"github.com/diffflow/server/internal/config"
	"github.com/diffflow/server/internal/files"
	"github.com/diffflow/server/internal/httpapi"
	"github.com/diffflow/server/internal/hub"
	"github.com/diffflow/server/internal/store"
)

func main() {
	configPath := flag.String("config", "./configs/default.toml", "配置文件路径")
	addrOverride := flag.String("addr", "", "覆盖配置文件中的监听地址")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("读取配置失败: %v", err)
	}
	if *addrOverride != "" {
		cfg.Server.Addr = *addrOverride
	}

	db, err := store.NewDB(cfg.Storage.SQLitePath)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()

	adminHash, err := auth.HashPassword(cfg.Admin.Password)
	if err != nil {
		log.Fatalf("初始化管理员密码失败: %v", err)
	}
	if _, err := db.EnsureConfiguredAdmin(cfg.Admin.Username, adminHash); err != nil {
		log.Fatalf("初始化管理员失败: %v", err)
	}
	if err := db.EnsureSetting(httpapi.MaxFileSettingKey(), strconv.FormatInt(cfg.Sync.MaxFileBytes, 10)); err != nil {
		log.Fatalf("初始化同步设置失败: %v", err)
	}

	fileStore, err := files.NewStore(cfg.Storage.FilesDir)
	if err != nil {
		log.Fatalf("初始化文件存储失败: %v", err)
	}

	tokens := auth.NewTokenManager(cfg.Security.SessionSecret)
	broker := hub.NewBroker()
	app := httpapi.New(db, tokens, broker, fileStore, cfg.Sync.MaxFileBytes)

	log.Printf("[DiffFlow Server] 数据库: %s", cfg.Storage.SQLitePath)
	log.Printf("[DiffFlow Server] 文件存储: %s", cfg.Storage.FilesDir)
	log.Printf("[DiffFlow Server] 监听 %s", cfg.Server.Addr)
	if err := http.ListenAndServe(cfg.Server.Addr, app.Routes()); err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
