# DiffFlow

DiffFlow 是一个面向 Godot 4 项目的实时协作插件。它由一个 Go 服务端和一个 Godot 编辑器插件组成，支持账号登录、项目权限、首次同步、实时文件同步和在线成员状态。

## 当前能力

- 服务端内置管理后台。
- 管理员账号和密码由配置文件指定。
- 管理员可创建用户、创建项目、分配用户可参与的项目。
- 管理员可生成授权密钥，用户可在插件端自行注册。
- 插件端登录后可选择项目，进入项目后自动执行首次同步。
- 文件内容通过 HTTP 上传/下载，WebSocket 只负责同步事件和在线状态。
- 默认不同步阈值为 100MB，管理员可在后台修改。

## 目录结构

```text
addons/diffflow/          Godot 编辑器插件
server/                   Go 服务端
server/configs/           服务端配置
scripts/deploy_linux.sh   Linux systemd 部署脚本
DEPLOY.md                 部署说明
USAGE_GUIDE.md            插件使用说明
```

## 服务端配置

默认配置文件在 `server/configs/default.toml`：

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

生产环境必须修改：

- `admin.password`
- `security.session_secret`

## 本地启动服务端

```powershell
cd server
go build -o diffflow-server.exe ./cmd/diffflow-server
.\diffflow-server.exe -config .\configs\default.toml
```

启动后打开：

```text
http://localhost:8090/admin
```

用配置文件中的管理员账号登录后台，然后创建项目、用户或授权密钥。

## 插件使用流程

1. 将 `addons/diffflow` 放入 Godot 项目根目录。
2. 在 Godot 插件设置中启用 `DiffFlow`。
3. 在右侧 DiffFlow 面板输入服务端地址，例如 `http://localhost:8090`。
4. 输入账号和密码，点击登录。
5. 如果管理员给的是授权密钥，填写账号、密码和授权密钥后点击注册。
6. 从项目下拉框选择项目。
7. 点击进入项目，插件会先做首次同步，然后进入实时监听。

## 同步机制

首次进入项目时，插件会请求服务端 manifest，并与本地文件比较：

- 服务端有、本地没有：下载。
- 本地有、服务端没有：上传。
- 两边都有但内容不同：mtime 较新的版本作为当前版本。

实时同步时：

- 本地文件变更通过 HTTP 上传到服务端。
- 服务端更新文件快照并通过 WebSocket 广播 `file_updated`。
- 其他客户端收到事件后通过 HTTP 下载文件。
- 删除文件会广播 `file_deleted`。

超过后台阈值的文件不会同步。默认阈值是 100MB。

## 默认忽略规则

插件不会同步：

- `.godot/`
- `.git/`
- `.import/`
- `.uid`
- `.import`、`.tmp`、`.remap` 后缀文件

## 开发验证

服务端测试：

```powershell
cd server
$env:GOCACHE='D:\Project\DiffFlow\.gocache'
$env:GOTELEMETRY='off'
go test ./...
```

当前测试覆盖了登录、授权密钥注册、项目权限、上传、manifest、下载和删除主流程。

## 部署

Linux 部署见 [DEPLOY.md](DEPLOY.md)。

插件使用说明见 [USAGE_GUIDE.md](USAGE_GUIDE.md)。
