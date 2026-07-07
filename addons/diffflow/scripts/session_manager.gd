@tool
extends RefCounted
class_name DFSessionManager

## Manages the collaborative editing session lifecycle.

signal session_connected
signal session_disconnected
signal peer_joined(peer_id: String, username: String)
signal peer_left(peer_id: String)

var _sync_engine  # DFSyncEngine (GDExtension)
var _server_url: String = ""
var _session_token: String = ""
var _project_id: int = 0
var _project_name: String = ""
var _is_connected: bool = false

func initialize(sync_engine) -> void:
	_sync_engine = sync_engine

func connect_to_project(url: String, token: String, project_id: int, project_name: String) -> int:
	_server_url = url
	_session_token = token
	_project_id = project_id
	_project_name = project_name
	if _sync_engine:
		_sync_engine.configure(url, token, project_id, project_name)
		var err: int = _sync_engine.connect_to_project()
		if err == OK:
			_is_connected = true
			session_connected.emit()
		return err
	return ERR_UNCONFIGURED

func disconnect_from_server() -> void:
	if _sync_engine:
		_sync_engine.disconnect_from_server()
	_is_connected = false
	session_disconnected.emit()

func is_connected_to_server() -> bool:
	return _is_connected
