# DiffFlow 部署文档

DiffFlow 服务端现在使用账号、项目和成员权限模型。管理员账号由配置文件指定，普通用户由管理员创建，或使用管理员生成的授权密钥自行注册。

## 配置

编辑 `server/configs/default.toml`：

```toml
[server]
addr = ":8090"

[admin]
username = "admin"
password = "change-me"

[storage]
sqlite_path = "./diffflow.db"
files_dir = "./data/files"

[sync]
max_file_mb = 100

[security]
session_secret = "change-me"
```

生产环境必须修改 `admin.password` 和 `security.session_secret`。

## 启动

```bash
cd server
go build -o diffflow-server ./cmd/diffflow-server
./diffflow-server -config ./configs/default.toml
```

启动后访问：

```text
http://服务器地址:8090/admin
```

在后台创建项目、用户，或生成授权密钥。

## Linux 部署

将以下内容上传到服务器同一目录：

- `diffflow-server-linux`
- `configs/default.toml`
- `scripts/deploy_linux.sh`

执行：

```bash
chmod +x deploy_linux.sh diffflow-server-linux
sudo ./deploy_linux.sh
```

管理服务：

```bash
sudo systemctl start diffflow
sudo systemctl stop diffflow
journalctl -u diffflow -f
```

## 客户端流程

1. 在 Godot 中启用 `addons/diffflow` 插件。
2. 在 DiffFlow 面板输入服务端 HTTP 地址，例如 `http://localhost:8090`。
3. 输入账号和密码登录。
4. 从项目下拉框选择项目。
5. 点击进入项目，插件会自动执行首次同步，然后进入实时监听。

## 文件同步

- 默认不同步阈值为 100MB。
- 管理员可在后台修改阈值。
- 阈值内文件通过 HTTP 文件通道传输，WebSocket 只广播同步事件和在线状态。
- `.godot`、`.git`、`.import`、`.uid`、临时文件不会同步。
