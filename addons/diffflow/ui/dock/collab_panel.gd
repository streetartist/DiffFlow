@tool
extends VBoxContainer

## Collaboration panel: login, project selection, and online members.

var sync_engine: DFSyncEngine

@onready var status_label: Label = $StatusBar/StatusLabel
@onready var login_btn: Button = $StatusBar/LoginBtn
@onready var register_btn: Button = $StatusBar/RegisterBtn
@onready var connect_btn: Button = $StatusBar/ConnectBtn
@onready var peer_list: ItemList = $PeerList
@onready var server_url_edit: LineEdit = $ConnectBar/ServerUrl
@onready var username_edit: LineEdit = $ConnectBar/Username
@onready var password_edit: LineEdit = $ConnectBar/Password
@onready var invite_key_edit: LineEdit = $ConnectBar/InviteKey
@onready var project_select: OptionButton = $ConnectBar/ProjectSelect

var _peers: Dictionary = {}
var _projects: Array = []
var _token: String = ""
const PEER_TIMEOUT := 6.0
var _cleanup_timer := 0.0

func _ready() -> void:
	login_btn.pressed.connect(_on_login_pressed)
	register_btn.pressed.connect(_on_register_pressed)
	connect_btn.pressed.connect(_on_connect_toggle)
	server_url_edit.text = DFSettings.get_server_url()
	username_edit.text = DFSettings.get_username()
	_token = DFSettings.get_session_token()
	_update_status()
	if not _token.is_empty():
		call_deferred("_fetch_projects")

func _process(delta: float) -> void:
	_cleanup_timer += delta
	if _cleanup_timer >= 3.0:
		_cleanup_timer = 0.0
		_cleanup_stale_peers()

func initialize(engine: DFSyncEngine) -> void:
	sync_engine = engine
	if sync_engine:
		sync_engine.connection_changed.connect(_on_connection_changed)
		sync_engine.presence_updated.connect(_on_presence_updated)
		_update_status()

func _on_connection_changed(_is_connected: bool) -> void:
	if not _is_connected:
		_peers.clear()
		_update_peer_list()
	_update_status()

func _on_login_pressed() -> void:
	var url := _normalize_server_url(server_url_edit.text)
	var username := username_edit.text.strip_edges()
	var password := password_edit.text
	if url.is_empty() or username.is_empty() or password.is_empty():
		_set_status("请输入服务器、账号和密码", Color.ORANGE)
		return
	DFSettings.set_server_url(url)
	DFSettings.set_username(username)
	var response: Dictionary = await _request_json(HTTPClient.METHOD_POST, "/api/login", {
		"username": username,
		"password": password
	}, false)
	await _accept_auth_response(response, "登录失败")

func _on_register_pressed() -> void:
	var url := _normalize_server_url(server_url_edit.text)
	var username := username_edit.text.strip_edges()
	var password := password_edit.text
	var invite_key := invite_key_edit.text.strip_edges()
	if url.is_empty() or username.is_empty() or password.is_empty() or invite_key.is_empty():
		_set_status("注册需要服务器、账号、密码和授权密钥", Color.ORANGE)
		return
	DFSettings.set_server_url(url)
	DFSettings.set_username(username)
	var response: Dictionary = await _request_json(HTTPClient.METHOD_POST, "/api/register", {
		"username": username,
		"password": password,
		"invite_key": invite_key
	}, false)
	await _accept_auth_response(response, "注册失败")

func _accept_auth_response(response: Dictionary, failure_prefix: String) -> void:
	if not response.get("ok", false):
		_set_status(failure_prefix + "：" + str(response.get("error", "unknown error")), Color.RED)
		return
	var data: Dictionary = response.get("data", {})
	_token = data.get("token", "")
	DFSettings.set_session_token(_token)
	password_edit.text = ""
	invite_key_edit.text = ""
	await _fetch_projects()

func _on_connect_toggle() -> void:
	if not sync_engine:
		return
	if sync_engine.is_server_connected():
		sync_engine.disconnect_from_server()
	else:
		if _token.is_empty():
			_set_status("请先登录", Color.ORANGE)
			return
		if _projects.is_empty():
			await _fetch_projects()
			if _projects.is_empty():
				return
		var selected := project_select.selected
		if selected < 0 or selected >= _projects.size():
			_set_status("请选择项目", Color.ORANGE)
			return
		var project: Dictionary = _projects[selected]
		var project_id := int(project.get("id", 0))
		var project_name := str(project.get("name", ""))
		DFSettings.set_project_id(project_id)
		DFSettings.set_project_name(project_name)
		sync_engine.configure(_normalize_server_url(server_url_edit.text), _token, project_id, project_name)
		var err := sync_engine.connect_to_project()
		if err != OK:
			_set_status("连接失败：%d" % err, Color.RED)
	_update_status()

func _on_presence_updated(peer_info: Dictionary) -> void:
	var pid: String = peer_info.get("peer_id", "")
	if pid.is_empty():
		return
	_peers[pid] = {
		"username": peer_info.get("username", pid.substr(0, 8)),
		"last_seen": Time.get_ticks_msec()
	}
	_update_peer_list()

func _cleanup_stale_peers() -> void:
	var now := Time.get_ticks_msec()
	var to_remove: Array = []
	for pid in _peers:
		var info: Dictionary = _peers[pid]
		if (now - info.last_seen) > PEER_TIMEOUT * 1000:
			to_remove.append(pid)
	for pid in to_remove:
		_peers.erase(pid)
	if to_remove.size() > 0:
		_update_peer_list()

func _update_status() -> void:
	var connected := sync_engine and sync_engine.is_server_connected()
	server_url_edit.editable = not connected
	username_edit.editable = not connected
	password_edit.editable = not connected
	invite_key_edit.editable = not connected
	project_select.disabled = connected
	login_btn.disabled = connected
	register_btn.disabled = connected
	if connected:
		_set_status("已连接：" + DFSettings.get_project_name(), Color.GREEN)
		connect_btn.text = "断开"
	else:
		if _token.is_empty():
			_set_status("未登录", Color.RED)
		elif _projects.is_empty():
			_set_status("已登录，暂无可用项目", Color.ORANGE)
		else:
			_set_status("已登录，请选择项目", Color(0.2, 0.55, 0.9))
		connect_btn.text = "进入项目"

func _update_peer_list() -> void:
	peer_list.clear()
	for pid in _peers:
		var info: Dictionary = _peers[pid]
		peer_list.add_item(info.username)

func _fetch_projects() -> void:
	var response: Dictionary = await _request_json(HTTPClient.METHOD_GET, "/api/projects", {}, true)
	if not response.get("ok", false):
		_projects.clear()
		project_select.clear()
		_token = ""
		DFSettings.set_session_token("")
		_set_status("项目加载失败：" + str(response.get("error", "unknown error")), Color.RED)
		return
	var data: Dictionary = response.get("data", {})
	DFSettings.set_max_file_bytes(int(data.get("max_file_bytes", 100 * 1024 * 1024)))
	_projects = data.get("projects", [])
	project_select.clear()
	var saved_project_id := DFSettings.get_project_id()
	var selected_index := 0
	for i in range(_projects.size()):
		var project: Dictionary = _projects[i]
		project_select.add_item(str(project.get("name", "Project")))
		if int(project.get("id", 0)) == saved_project_id:
			selected_index = i
	if _projects.size() > 0:
		project_select.select(selected_index)
	_update_status()

func _request_json(method: int, path: String, body: Dictionary = {}, with_auth: bool = true) -> Dictionary:
	var http := HTTPRequest.new()
	add_child(http)
	var headers := PackedStringArray()
	if with_auth and not _token.is_empty():
		headers.append("Authorization: Bearer " + _token)
	var payload := ""
	if not body.is_empty():
		headers.append("Content-Type: application/json")
		payload = JSON.stringify(body)
	var err := http.request(_normalize_server_url(server_url_edit.text) + path, headers, method, payload)
	if err != OK:
		http.queue_free()
		return {"ok": false, "error": "request failed: %d" % err}
	var completed: Array = await http.request_completed
	http.queue_free()
	var result: int = completed[0]
	var code: int = completed[1]
	var response_body: PackedByteArray = completed[3]
	if result != HTTPRequest.RESULT_SUCCESS:
		return {"ok": false, "status": code, "error": "network error: %d" % result}
	var text := response_body.get_string_from_utf8()
	var parsed: Variant = JSON.parse_string(text)
	if code < 200 or code >= 300:
		var message := text
		if parsed is Dictionary and parsed.has("error"):
			message = parsed.get("error", message)
		return {"ok": false, "status": code, "error": message}
	if parsed == null:
		return {"ok": false, "error": "invalid json response"}
	return {"ok": true, "status": code, "data": parsed}

func _set_status(text: String, color: Color) -> void:
	status_label.text = text
	status_label.add_theme_color_override("font_color", color)

func _normalize_server_url(url: String) -> String:
	url = url.strip_edges()
	while url.ends_with("/"):
		url = url.substr(0, url.length() - 1)
	return url
