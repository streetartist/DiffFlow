@tool
extends RefCounted
class_name DFSettings

## Persistent plugin settings stored in EditorSettings.

const SETTING_PREFIX := "diffflow/"
const DEFAULT_SERVER_URL := "http://localhost:8090"

static func get_server_url() -> String:
	var es := EditorInterface.get_editor_settings()
	if es.has_setting(SETTING_PREFIX + "server_url"):
		return es.get_setting(SETTING_PREFIX + "server_url")
	return DEFAULT_SERVER_URL

static func set_server_url(url: String) -> void:
	var es := EditorInterface.get_editor_settings()
	es.set_setting(SETTING_PREFIX + "server_url", url)

static func get_username() -> String:
	var es := EditorInterface.get_editor_settings()
	if es.has_setting(SETTING_PREFIX + "username"):
		return es.get_setting(SETTING_PREFIX + "username")
	return ""

static func set_username(username: String) -> void:
	var es := EditorInterface.get_editor_settings()
	es.set_setting(SETTING_PREFIX + "username", username)

static func get_session_token() -> String:
	var es := EditorInterface.get_editor_settings()
	if es.has_setting(SETTING_PREFIX + "session_token"):
		return es.get_setting(SETTING_PREFIX + "session_token")
	return ""

static func set_session_token(token: String) -> void:
	var es := EditorInterface.get_editor_settings()
	es.set_setting(SETTING_PREFIX + "session_token", token)

static func get_project_id() -> int:
	var es := EditorInterface.get_editor_settings()
	if es.has_setting(SETTING_PREFIX + "project_id"):
		return int(es.get_setting(SETTING_PREFIX + "project_id"))
	return 0

static func set_project_id(project_id: int) -> void:
	var es := EditorInterface.get_editor_settings()
	es.set_setting(SETTING_PREFIX + "project_id", project_id)

static func get_project_name() -> String:
	var es := EditorInterface.get_editor_settings()
	if es.has_setting(SETTING_PREFIX + "project_name"):
		return es.get_setting(SETTING_PREFIX + "project_name")
	return ""

static func set_project_name(project_name: String) -> void:
	var es := EditorInterface.get_editor_settings()
	es.set_setting(SETTING_PREFIX + "project_name", project_name)

static func get_max_file_bytes() -> int:
	var es := EditorInterface.get_editor_settings()
	if es.has_setting(SETTING_PREFIX + "max_file_bytes"):
		return int(es.get_setting(SETTING_PREFIX + "max_file_bytes"))
	return 100 * 1024 * 1024

static func set_max_file_bytes(max_file_bytes: int) -> void:
	var es := EditorInterface.get_editor_settings()
	es.set_setting(SETTING_PREFIX + "max_file_bytes", max_file_bytes)

static func get_client_id() -> String:
	var es := EditorInterface.get_editor_settings()
	var key := SETTING_PREFIX + "client_id"
	if es.has_setting(key):
		var saved := str(es.get_setting(key))
		if not saved.is_empty():
			return saved
	var client_id := _generate_id("client")
	es.set_setting(key, client_id)
	return client_id

static func _generate_id(prefix: String) -> String:
	var crypto := Crypto.new()
	return prefix + "-" + crypto.generate_random_bytes(16).hex_encode()
