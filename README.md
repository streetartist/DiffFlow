# DiffFlow

DiffFlow 是一个面向 Godot 4 项目的实时协作插件。它由一个 Go 服务端和一个 Godot 编辑器插件组成，支持账号登录、项目权限、首次同步、实时文件同步和在线成员状态。

## 当前能力

- 服务端内置管理后台。
- 管理员账号和密码由配置文件指定。
- 管理员可创建用户、创建项目、分配用户可参与的项目。
- 管理员可生成授权密钥，用户可在插件端自行注册。
- 插件端登录后可选择项目，进入项目后自动执行首次同步。
- 文件内容通过 HTTP 上传/下载，WebSocket 只负责同步事件和在线状态。
- 服务端会登记已打开场景；同一场景被他人占用时，需要请求接管，对方许可后才会从最新版继续编辑。
- 上传和删除都会携带 `base_sha`，服务端发现远端已被别人更新时返回 `409 conflict`，避免静默覆盖。
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
- 上传和删除请求必须携带本地已知的 `base_sha`。
- 如果服务端当前版本和 `base_sha` 不一致，会拒绝本次写入并返回 `409 conflict`。
- 服务端更新文件快照并通过 WebSocket 广播 `file_updated`。
- 其他客户端收到事件后通过 HTTP 下载文件。
- 删除文件会广播 `file_deleted`。

超过后台阈值的文件不会同步。默认阈值是 100MB。

## 场景协作与接管

插件会周期性向服务端登记当前打开的 `.tscn` / `.scn` 场景。服务端同一时间只允许一个编辑器实例占用同一个场景路径：

- A 打开场景后，服务端登记 A 为当前占用者。
- B 打开同一场景时，会收到“场景被占用”提示。
- B 可以放弃编辑并关闭场景，也可以向 A 请求接管。
- B 请求接管后会先关闭本地场景，等待 A 处理。
- A 许可后，插件会自动保存 A 的当前场景，上传成功后关闭 A 的场景窗口。
- 上传成功后服务端才会通知 B；B 会下载最新版并重新打开该场景，从最新版本开始编辑。
- 如果 A 保存失败、上传失败或遇到 `base_sha` 冲突，请求会被拒绝，不会放行给 B。

这个流程用于避免两个人同时在同一个场景上原地修改。普通文件仍按文件同步和 `base_sha` 冲突检测处理。

## 同步排除规则

插件内置只排除编辑器和版本控制的内部状态目录：

- `.godot/`
- `.git/`

其他排除都通过项目根目录的 `.diffflowignore` 配置。这个文件会像普通项目文件一样同步给团队，支持常用 ignore 写法：

- 空行和 `#` 注释
- `*`、`?` 通配符
- `path/` 目录规则
- `/path` 项目根路径规则
- `!path` 反选规则

本仓库的 `.diffflowignore` 排除了自托管服务端运行时文件，例如 `server/diffflow.db`、`server/diffflow.db-wal`、`server/data/` 和日志。普通用户项目里的 `.db` 文件不会被默认忽略，除非用户自己在 `.diffflowignore` 中写规则。

## 开发验证

服务端测试：

```powershell
cd server
$env:GOCACHE='D:\Project\DiffFlow\.gocache'
$env:GOTELEMETRY='off'
go test ./...
```

当前测试覆盖了登录、授权密钥注册、项目权限、上传、manifest、下载、删除、`base_sha` 冲突和场景占用状态。

## 部署

Linux 部署见 [DEPLOY.md](DEPLOY.md)。

插件使用说明见 [USAGE_GUIDE.md](USAGE_GUIDE.md)。
