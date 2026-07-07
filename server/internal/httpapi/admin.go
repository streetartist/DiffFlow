package httpapi

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/diffflow/server/internal/auth"
	"github.com/diffflow/server/internal/store"
)

type adminPageData struct {
	Admin     *store.User
	Users     []store.UserWithProjects
	Projects  []store.Project
	Invites   []store.Invite
	MaxFileMB int64
	Error     string
	Message   string
}

func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		renderAdminLogin(w, "")
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			renderAdminLogin(w, "表单无效")
			return
		}
		username := strings.TrimSpace(r.FormValue("username"))
		password := r.FormValue("password")
		user, err := s.db.GetUserByUsername(username)
		if err != nil || !user.Enabled || !user.IsAdmin || !auth.VerifyPassword(user.PasswordHash, password) {
			renderAdminLogin(w, "管理员账号或密码错误")
			return
		}
		token, err := issueLoginToken(s.tokens, user)
		if err != nil {
			renderAdminLogin(w, "无法创建登录会话")
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "df_token",
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Expires:  time.Now().Add(24 * time.Hour),
		})
		http.Redirect(w, r, "/admin", http.StatusFound)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "df_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/admin/login", http.StatusFound)
}

func (s *Server) handleAdminDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin" {
		http.NotFound(w, r)
		return
	}
	admin, ok := s.adminFromRequest(r)
	if !ok {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	data, err := s.adminData(admin)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data.Error = r.URL.Query().Get("error")
	data.Message = r.URL.Query().Get("message")
	renderAdminDashboard(w, data)
}

func (s *Server) handleAdminAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if _, ok := s.adminFromRequest(r); !ok {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		redirectAdmin(w, r, "", "表单无效")
		return
	}

	var err error
	switch r.URL.Path {
	case "/admin/settings":
		err = s.updateSettings(r)
	case "/admin/projects/create":
		err = s.createProject(r)
	case "/admin/users/create":
		err = s.createUser(r)
	case "/admin/users/projects":
		err = s.updateUserProjects(r)
	case "/admin/users/password":
		err = s.resetUserPassword(r)
	case "/admin/users/toggle":
		err = s.toggleUser(r)
	case "/admin/invites/create":
		err = s.createInvite(r)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		redirectAdmin(w, r, "", err.Error())
		return
	}
	redirectAdmin(w, r, "已保存", "")
}

func (s *Server) adminFromRequest(r *http.Request) (*store.User, bool) {
	cookie, err := r.Cookie("df_token")
	if err != nil || cookie.Value == "" {
		return nil, false
	}
	claims, err := s.tokens.Parse(cookie.Value)
	if err != nil {
		return nil, false
	}
	user, err := s.db.GetUserByID(claims.UserID)
	if err != nil || !user.Enabled || !user.IsAdmin {
		return nil, false
	}
	return user, true
}

func (s *Server) adminData(admin *store.User) (*adminPageData, error) {
	users, err := s.db.ListUsers()
	if err != nil {
		return nil, err
	}
	projects, err := s.db.ListProjects()
	if err != nil {
		return nil, err
	}
	invites, err := s.db.ListInvites()
	if err != nil {
		return nil, err
	}
	maxFileBytes, err := s.maxFileBytes()
	if err != nil {
		return nil, err
	}
	return &adminPageData{
		Admin:     admin,
		Users:     users,
		Projects:  projects,
		Invites:   invites,
		MaxFileMB: humanMB(maxFileBytes),
	}, nil
}

func (s *Server) updateSettings(r *http.Request) error {
	maxMB := parseInt64Form(r, "max_file_mb", 100)
	if maxMB <= 0 {
		return fmt.Errorf("同步阈值必须大于 0")
	}
	return s.db.SetSetting(maxFileSettingKey, strconv.FormatInt(maxMB*1024*1024, 10))
}

func (s *Server) createProject(r *http.Request) error {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		return fmt.Errorf("项目名称不能为空")
	}
	_, err := s.db.CreateProject(name)
	return err
}

func (s *Server) createUser(r *http.Request) error {
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	if username == "" || password == "" {
		return fmt.Errorf("用户名和密码不能为空")
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	_, err = s.db.CreateUser(username, hash, selectedProjectIDs(r))
	return err
}

func (s *Server) updateUserProjects(r *http.Request) error {
	userID := parseInt64Form(r, "user_id", 0)
	if userID <= 0 {
		return fmt.Errorf("用户无效")
	}
	return s.db.UpdateUserProjects(userID, selectedProjectIDs(r))
}

func (s *Server) resetUserPassword(r *http.Request) error {
	userID := parseInt64Form(r, "user_id", 0)
	password := r.FormValue("password")
	if userID <= 0 || password == "" {
		return fmt.Errorf("用户和新密码不能为空")
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	return s.db.SetUserPassword(userID, hash)
}

func (s *Server) toggleUser(r *http.Request) error {
	userID := parseInt64Form(r, "user_id", 0)
	enabled := r.FormValue("enabled") == "1"
	if userID <= 0 {
		return fmt.Errorf("用户无效")
	}
	return s.db.SetUserEnabled(userID, enabled)
}

func (s *Server) createInvite(r *http.Request) error {
	maxUses := int(parseInt64Form(r, "max_uses", 1))
	if maxUses <= 0 {
		return fmt.Errorf("可用次数必须大于 0")
	}
	key, err := randomKey()
	if err != nil {
		return err
	}
	expiresAt := parseExpires(parseInt64Form(r, "expires_days", 0))
	return s.db.CreateInvite(key, maxUses, expiresAt, selectedProjectIDs(r))
}

func randomKey() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func redirectAdmin(w http.ResponseWriter, r *http.Request, message, errText string) {
	values := url.Values{}
	if message != "" {
		values.Set("message", message)
	}
	if errText != "" {
		values.Set("error", errText)
	}
	target := "/admin"
	if encoded := values.Encode(); encoded != "" {
		target += "?" + encoded
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func renderAdminLogin(w http.ResponseWriter, errText string) {
	tmpl := template.Must(template.New("login").Parse(adminLoginTemplate))
	_ = tmpl.Execute(w, map[string]string{"Error": errText})
}

func renderAdminDashboard(w http.ResponseWriter, data *adminPageData) {
	funcs := template.FuncMap{
		"hasProject": func(projects []store.Project, id int64) bool {
			return projectIDSet(projects)[id]
		},
		"joinProjects": func(projects []store.Project) string {
			if len(projects) == 0 {
				return "未分配项目"
			}
			names := make([]string, 0, len(projects))
			for _, project := range projects {
				names = append(names, project.Name)
			}
			return strings.Join(names, ", ")
		},
		"expiryText": func(ts int64) string {
			if ts <= 0 {
				return "永不过期"
			}
			return time.Unix(ts, 0).Format("2006-01-02")
		},
		"sub": func(a, b int) int {
			return a - b
		},
	}
	tmpl := template.Must(template.New("admin").Funcs(funcs).Parse(adminDashboardTemplate))
	_ = tmpl.Execute(w, data)
}

const adminLoginTemplate = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>DiffFlow 管理后台</title>
  <style>
    body{margin:0;min-height:100vh;display:grid;place-items:center;background:#f5f7fb;color:#1f2937;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
    .panel{width:min(380px,calc(100vw - 32px));background:white;border:1px solid #d9e0ea;border-radius:8px;box-shadow:0 18px 60px rgba(15,23,42,.12);padding:28px}
    h1{margin:0 0 6px;font-size:24px}.muted{margin:0 0 22px;color:#667085}
    label{display:block;margin:14px 0 6px;font-weight:600;font-size:13px}
    input{width:100%;box-sizing:border-box;border:1px solid #cbd5e1;border-radius:6px;padding:11px 12px;font-size:14px}
    button{width:100%;margin-top:20px;border:0;border-radius:6px;background:#2563eb;color:white;padding:11px 14px;font-weight:700;cursor:pointer}
    .error{background:#fef2f2;border:1px solid #fecaca;color:#991b1b;border-radius:6px;padding:10px;margin-bottom:14px}
  </style>
</head>
<body>
  <form class="panel" method="post" action="/admin/login">
    <h1>DiffFlow</h1>
    <p class="muted">管理后台</p>
    {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
    <label>管理员账号</label>
    <input name="username" autocomplete="username" autofocus>
    <label>密码</label>
    <input name="password" type="password" autocomplete="current-password">
    <button type="submit">登录</button>
  </form>
</body>
</html>`

const adminDashboardTemplate = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>DiffFlow 管理后台</title>
  <style>
    :root{--bg:#f4f6f8;--panel:#fff;--line:#d7dee8;--text:#202938;--muted:#667085;--blue:#2563eb;--green:#15803d;--red:#b42318}
    *{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
    header{height:58px;background:#111827;color:white;display:flex;align-items:center;justify-content:space-between;padding:0 28px}
    header strong{font-size:18px}header a{color:#dbeafe;text-decoration:none}
    main{max-width:1180px;margin:24px auto 48px;padding:0 20px;display:grid;gap:18px}
    .grid{display:grid;grid-template-columns:1fr 1fr;gap:18px}.card{background:var(--panel);border:1px solid var(--line);border-radius:8px;padding:18px}
    h2{font-size:18px;margin:0 0 14px}h3{font-size:15px;margin:18px 0 10px;color:#344054}
    label{display:block;margin:10px 0 5px;font-weight:650;font-size:13px}input{border:1px solid #cbd5e1;border-radius:6px;padding:9px 10px;font-size:14px;width:100%}
    button{border:0;border-radius:6px;background:var(--blue);color:white;padding:9px 12px;font-weight:700;cursor:pointer}.secondary{background:#475467}.danger{background:#b42318}.ghost{background:#eef2f7;color:#344054}
    table{width:100%;border-collapse:collapse;font-size:14px}th,td{text-align:left;border-top:1px solid var(--line);padding:10px 8px;vertical-align:top}th{color:#475467;font-size:12px;text-transform:uppercase;letter-spacing:.04em}
    .row{display:flex;gap:10px;align-items:end}.row>*{flex:1}.checks{display:grid;grid-template-columns:repeat(auto-fit,minmax(140px,1fr));gap:8px;margin:8px 0 12px}
    .check{display:flex;gap:7px;align-items:center;color:#344054;font-size:13px}.check input{width:auto}.muted{color:var(--muted)}.ok{color:var(--green)}.bad{color:var(--red)}
    .notice{border-radius:6px;padding:10px 12px}.notice.ok{background:#ecfdf3;border:1px solid #abefc6}.notice.bad{background:#fef3f2;border:1px solid #fecdca}
    form.inline{display:inline}.actions{display:flex;gap:8px;flex-wrap:wrap}.stack{display:grid;gap:10px}
    @media (max-width:900px){.grid{grid-template-columns:1fr}header{padding:0 16px}main{padding:0 14px}}
  </style>
</head>
<body>
  <header><strong>DiffFlow 管理后台</strong><span>{{.Admin.Username}} · <a href="/admin/logout">退出</a></span></header>
  <main>
    {{if .Message}}<div class="notice ok">{{.Message}}</div>{{end}}
    {{if .Error}}<div class="notice bad">{{.Error}}</div>{{end}}

    <section class="grid">
      <div class="card">
        <h2>同步设置</h2>
        <form method="post" action="/admin/settings" class="row">
          <div><label>不同步阈值 MB</label><input name="max_file_mb" type="number" min="1" value="{{.MaxFileMB}}"></div>
          <div><button type="submit">保存设置</button></div>
        </form>
        <p class="muted">默认 100MB。超过阈值的文件不会同步，阈值以内通过 HTTP 文件通道传输，WebSocket 只负责事件通知。</p>
      </div>

      <div class="card">
        <h2>新建项目</h2>
        <form method="post" action="/admin/projects/create" class="row">
          <div><label>项目名称</label><input name="name" placeholder="Godot 项目名"></div>
          <div><button type="submit">创建项目</button></div>
        </form>
      </div>
    </section>

    <section class="card">
      <h2>用户</h2>
      <form method="post" action="/admin/users/create">
        <div class="row">
          <div><label>用户名</label><input name="username"></div>
          <div><label>初始密码</label><input name="password" type="password"></div>
          <div><button type="submit">添加用户</button></div>
        </div>
        <div class="checks">
          {{range .Projects}}<label class="check"><input type="checkbox" name="project_ids" value="{{.ID}}">{{.Name}}</label>{{end}}
        </div>
      </form>

      <table>
        <thead><tr><th>用户</th><th>状态</th><th>项目</th><th>管理</th></tr></thead>
        <tbody>
        {{range .Users}}
          {{$user := .}}
          <tr>
            <td><strong>{{.Username}}</strong>{{if .IsAdmin}}<div class="muted">配置管理员</div>{{end}}</td>
            <td>{{if .Enabled}}<span class="ok">启用</span>{{else}}<span class="bad">禁用</span>{{end}}</td>
            <td>
              <form method="post" action="/admin/users/projects" class="stack">
                <input type="hidden" name="user_id" value="{{.ID}}">
                <div class="checks">
                  {{range $project := $.Projects}}
                    <label class="check"><input type="checkbox" name="project_ids" value="{{$project.ID}}" {{if hasProject $user.Projects $project.ID}}checked{{end}}>{{$project.Name}}</label>
                  {{end}}
                </div>
                <span class="muted">当前：{{joinProjects .Projects}}</span>
                {{if not .IsAdmin}}<button class="ghost" type="submit">保存项目权限</button>{{end}}
              </form>
            </td>
            <td class="actions">
              {{if not .IsAdmin}}
                <form method="post" action="/admin/users/password" class="inline">
                  <input type="hidden" name="user_id" value="{{.ID}}">
                  <input name="password" type="password" placeholder="新密码">
                  <button class="secondary" type="submit">重置密码</button>
                </form>
                <form method="post" action="/admin/users/toggle" class="inline">
                  <input type="hidden" name="user_id" value="{{.ID}}">
                  {{if .Enabled}}<input type="hidden" name="enabled" value="0"><button class="danger" type="submit">禁用</button>{{else}}<input type="hidden" name="enabled" value="1"><button type="submit">启用</button>{{end}}
                </form>
              {{else}}<span class="muted">管理员由配置文件控制</span>{{end}}
            </td>
          </tr>
        {{end}}
        </tbody>
      </table>
    </section>

    <section class="grid">
      <div class="card">
        <h2>授权密钥</h2>
        <form method="post" action="/admin/invites/create">
          <div class="row">
            <div><label>可用次数</label><input name="max_uses" type="number" min="1" value="1"></div>
            <div><label>过期天数</label><input name="expires_days" type="number" min="0" value="7"></div>
            <div><button type="submit">生成密钥</button></div>
          </div>
          <div class="checks">
            {{range .Projects}}<label class="check"><input type="checkbox" name="project_ids" value="{{.ID}}">{{.Name}}</label>{{end}}
          </div>
        </form>
      </div>

      <div class="card">
        <h2>项目列表</h2>
        <table><thead><tr><th>ID</th><th>名称</th></tr></thead><tbody>{{range .Projects}}<tr><td>{{.ID}}</td><td>{{.Name}}</td></tr>{{end}}</tbody></table>
      </div>
    </section>

    <section class="card">
      <h2>已生成密钥</h2>
      <table>
        <thead><tr><th>密钥</th><th>使用</th><th>过期</th><th>项目</th></tr></thead>
        <tbody>
        {{range .Invites}}
          <tr><td><code>{{.Key}}</code></td><td>{{.Uses}} / {{.MaxUses}}</td><td>{{expiryText .ExpiresAt}}</td><td>{{joinProjects .Projects}}</td></tr>
        {{end}}
        </tbody>
      </table>
    </section>
  </main>
</body>
</html>`
