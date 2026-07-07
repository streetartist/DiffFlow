@tool
extends Node
class_name DFSyncEngine

## WebSocket event engine. File bytes are transferred through HTTP APIs.

signal remote_file_updated(payload: Dictionary)
signal remote_file_deleted(payload: Dictionary)
signal remote_scene_opened(payload: Dictionary)
signal remote_scene_busy(payload: Dictionary)
signal remote_scene_takeover_requested(payload: Dictionary)
signal remote_scene_takeover_approved(payload: Dictionary)
signal remote_scene_takeover_denied(payload: Dictionary)
signal presence_updated(peer_info: Dictionary)
signal connection_changed(connected: bool)

var _ws := WebSocketPeer.new()
var _connected := false
var _peer_id: String = ""
var _client_id: String = ""
var _username: String = ""
var _session_token: String = ""
var _project_id: int = 0
var _project_name: String = ""

# Auto-reconnect
var _server_url: String = ""
var _auto_reconnect := true
var _reconnect_timer := 0.0
const RECONNECT_INTERVAL := 3.0
var _was_connected := false

# Presence heartbeat
var _presence_timer := 0.0
const PRESENCE_INTERVAL := 2.0

func _ready() -> void:
	_client_id = DFSettings.get_client_id()
	_peer_id = _generate_peer_id()
	_username = DFSettings.get_username()

func _process(delta: float) -> void:
	var state := _ws.get_ready_state()

	if state == WebSocketPeer.STATE_CONNECTING or state == WebSocketPeer.STATE_OPEN:
		_ws.poll()
		state = _ws.get_ready_state()

	if state == WebSocketPeer.STATE_OPEN:
		if not _connected:
			_connected = true
			_was_connected = true
			_reconnect_timer = 0.0
			print("[DiffFlow] Connected to server.")
			connection_changed.emit(true)
		while _ws.get_available_packet_count() > 0:
			_on_message(_ws.get_packet())
		# Send presence heartbeat
		_presence_timer += delta
		if _presence_timer >= PRESENCE_INTERVAL:
			_presence_timer = 0.0
			_send_presence()
	elif state == WebSocketPeer.STATE_CLOSED or state == WebSocketPeer.STATE_CLOSING:
		if _connected:
			_connected = false
			print("[DiffFlow] Disconnected.")
			connection_changed.emit(false)
		if _auto_reconnect and not _server_url.is_empty() and _was_connected:
			_reconnect_timer += delta
			if _reconnect_timer >= RECONNECT_INTERVAL:
				_reconnect_timer = 0.0
				_ws = WebSocketPeer.new()
				_ws.connect_to_url(_build_ws_url())

func configure(server_url: String, token: String, project_id: int, project_name: String) -> void:
	_server_url = _normalize_server_url(server_url)
	_session_token = token
	_project_id = project_id
	_project_name = project_name
	_username = DFSettings.get_username()

func _build_ws_url() -> String:
	var ws_url := _server_url
	if ws_url.begins_with("https://"):
		ws_url = "wss://" + ws_url.substr(8)
	elif ws_url.begins_with("http://"):
		ws_url = "ws://" + ws_url.substr(7)
	if not ws_url.ends_with("/ws"):
		ws_url += "/ws"
	var sep := "&" if "?" in ws_url else "?"
	return ws_url + sep + "token=" + _session_token.uri_encode() + "&project_id=" + str(_project_id) + "&peer_id=" + _peer_id.uri_encode()

func connect_to_project() -> int:
	if _server_url.is_empty() or _session_token.is_empty() or _project_id <= 0:
		return ERR_UNCONFIGURED
	if _client_id.is_empty():
		_client_id = DFSettings.get_client_id()
	if _peer_id.is_empty():
		_peer_id = _generate_peer_id()
	_was_connected = false
	_auto_reconnect = true
	_ws = WebSocketPeer.new()
	var err := _ws.connect_to_url(_build_ws_url())
	if err == OK:
		_was_connected = true
	return err

func disconnect_from_server() -> void:
	_auto_reconnect = false
	_ws.close()
	_connected = false
	_server_url = ""
	_was_connected = false
	connection_changed.emit(false)

func is_server_connected() -> bool:
	return _connected

func get_peer_id() -> String:
	return _peer_id

func get_client_id() -> String:
	return _client_id

func get_project_id() -> int:
	return _project_id

func get_server_url() -> String:
	return _server_url

func get_session_token() -> String:
	return _session_token

func send_text(data: String) -> void:
	if _connected:
		_ws.send_text(data)

func send_scene_opened(rel_path: String) -> void:
	_send_scene_event("scene_opened", rel_path)

func send_scene_released(rel_path: String) -> void:
	_send_scene_event("scene_released", rel_path)

func send_scene_takeover_request(rel_path: String, target_peer_id: String) -> void:
	_send_scene_event("scene_takeover_request", rel_path, target_peer_id)

func send_scene_takeover_approved(rel_path: String, target_peer_id: String, sha256: String = "") -> void:
	var extra := {}
	if not sha256.is_empty():
		extra["sha256"] = sha256
	_send_scene_event("scene_takeover_approved", rel_path, target_peer_id, extra)

func send_scene_takeover_denied(rel_path: String, target_peer_id: String, reason: String = "") -> void:
	var extra := {}
	if not reason.is_empty():
		extra["reason"] = reason
	_send_scene_event("scene_takeover_denied", rel_path, target_peer_id, extra)

func _send_scene_event(event_type: String, rel_path: String, target_peer_id: String = "", extra: Dictionary = {}) -> void:
	rel_path = rel_path.replace("\\", "/").strip_edges()
	if not _connected or rel_path.is_empty():
		return
	if not (rel_path.ends_with(".tscn") or rel_path.ends_with(".scn")):
		return
	var msg := {
		"type": event_type,
		"path": rel_path,
		"peer_id": _peer_id,
		"client_id": _client_id,
		"username": _username,
		"project_id": _project_id,
		"project_name": _project_name,
	}
	if not target_peer_id.is_empty():
		msg["target_peer_id"] = target_peer_id
	for key in extra.keys():
		msg[key] = extra[key]
	_ws.send_text(JSON.stringify(msg))

func _send_presence() -> void:
	var msg := {
		"type": "presence",
		"peer_id": _peer_id,
		"client_id": _client_id,
		"username": _username,
		"project_id": _project_id,
		"project_name": _project_name,
		"status": "online"
	}
	_ws.send_text(JSON.stringify(msg))

func _on_message(data: PackedByteArray) -> void:
	var text := data.get_string_from_utf8()
	var event: Variant = JSON.parse_string(text)
	if not event is Dictionary:
		return
	var sender: String = event.get("peer_id", "")
	if sender == _peer_id:
		return
	var target_peer_id: String = event.get("target_peer_id", "")
	if not target_peer_id.is_empty() and target_peer_id != _peer_id:
		return
	var msg_type: String = event.get("type", "")
	match msg_type:
		"file_updated":
			remote_file_updated.emit(event)
		"file_deleted":
			remote_file_deleted.emit(event)
		"scene_opened":
			remote_scene_opened.emit(event)
		"scene_busy":
			remote_scene_busy.emit(event)
		"scene_takeover_request":
			remote_scene_takeover_requested.emit(event)
		"scene_takeover_approved":
			remote_scene_takeover_approved.emit(event)
		"scene_takeover_denied":
			remote_scene_takeover_denied.emit(event)
		"presence":
			presence_updated.emit(event)

func _generate_peer_id() -> String:
	var crypto := Crypto.new()
	return "session-" + crypto.generate_random_bytes(16).hex_encode()

func _normalize_server_url(url: String) -> String:
	url = url.strip_edges()
	while url.ends_with("/"):
		url = url.substr(0, url.length() - 1)
	return url
