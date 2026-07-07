@tool
extends EditorPlugin

## DiffFlow: 实时协同编辑 + Git 版本管理。

var main_dock: Control
var _sync_engine: DFSyncEngine
var _file_watcher: DFFileWatcher
var _scene_poll_timer := 0.0
var _open_scene_cache: Dictionary = {}
var _scene_notice_cache: Dictionary = {}
var _takeover_waiting: Dictionary = {}
var _takeover_granting: Dictionary = {}

const SCENE_POLL_INTERVAL := 0.5

func _enter_tree() -> void:
	_sync_engine = DFSyncEngine.new()
	_sync_engine.name = "DFSyncEngine"
	add_child(_sync_engine)

	_file_watcher = DFFileWatcher.new()
	_file_watcher.name = "DFFileWatcher"
	_file_watcher.sync_engine = _sync_engine
	_file_watcher.plugin = self
	_file_watcher.sync_conflict.connect(_on_sync_conflict)
	add_child(_file_watcher)

	_sync_engine.remote_file_updated.connect(_on_remote_file_updated)
	_sync_engine.remote_file_deleted.connect(_on_remote_file_deleted)
	_sync_engine.remote_scene_opened.connect(_on_remote_scene_opened)
	_sync_engine.remote_scene_busy.connect(_on_remote_scene_busy)
	_sync_engine.remote_scene_takeover_requested.connect(_on_remote_scene_takeover_requested)
	_sync_engine.remote_scene_takeover_approved.connect(_on_remote_scene_takeover_approved)
	_sync_engine.remote_scene_takeover_denied.connect(_on_remote_scene_takeover_denied)
	_sync_engine.connection_changed.connect(_on_connection_changed)

	main_dock = preload("res://addons/diffflow/ui/dock/main_dock.tscn").instantiate()
	main_dock.sync_engine = _sync_engine
	add_control_to_dock(DOCK_SLOT_RIGHT_UL, main_dock)

	call_deferred("_auto_connect")
	print("[DiffFlow] 插件已加载。")

func _exit_tree() -> void:
	_file_watcher.stop()
	if main_dock:
		remove_control_from_docks(main_dock)
		main_dock.queue_free()
	for child in [_file_watcher, _sync_engine]:
		if child:
			child.queue_free()

func _process(delta: float) -> void:
	if not _sync_engine or not _sync_engine.is_server_connected():
		return
	_scene_poll_timer += delta
	if _scene_poll_timer < SCENE_POLL_INTERVAL:
		return
	_scene_poll_timer = 0.0
	_broadcast_open_scenes_if_changed()

func _auto_connect() -> void:
	var url := DFSettings.get_server_url()
	var token := DFSettings.get_session_token()
	var project_id := DFSettings.get_project_id()
	var project_name := DFSettings.get_project_name()
	if not token.is_empty() and project_id > 0:
		_sync_engine.configure(url, token, project_id, project_name)
		_sync_engine.connect_to_project()

func _on_connection_changed(connected: bool) -> void:
	if connected:
		var project_path := ProjectSettings.globalize_path("res://")
		_file_watcher.configure(
			_sync_engine.get_server_url(),
			_sync_engine.get_session_token(),
			_sync_engine.get_project_id(),
			DFSettings.get_max_file_bytes()
		)
		_file_watcher.start(project_path)
		call_deferred("_broadcast_open_scenes_if_changed", true)
	else:
		_file_watcher.stop()
		_open_scene_cache.clear()

func _on_remote_file_updated(payload: Dictionary) -> void:
	var rel_path: String = payload.get("path", "")
	var res_path: String = "res://" + rel_path
	if _takeover_waiting.has(rel_path):
		_file_watcher.apply_remote_update(payload)
		return
	if _is_open_scene(res_path):
		_ask_merge(rel_path, payload)
	else:
		_file_watcher.apply_remote_update(payload)

func _on_remote_file_deleted(payload: Dictionary) -> void:
	var rel_path: String = payload.get("path", "")
	var res_path: String = "res://" + rel_path
	if _is_open_scene(res_path):
		_show_info("文件 \"%s\" 被其他人删除了，场景将关闭。" % rel_path.get_file())
		_file_watcher.apply_remote_delete(payload)
		call_deferred("_close_scene")
	else:
		_file_watcher.apply_remote_delete(payload)

func _on_remote_scene_opened(payload: Dictionary) -> void:
	pass

func _on_remote_scene_busy(payload: Dictionary) -> void:
	var rel_path: String = payload.get("path", "")
	if rel_path.is_empty() or _takeover_waiting.has(rel_path):
		return
	if not _is_open_scene_rel_path(rel_path):
		return
	var owner_peer_id := str(payload.get("owner_peer_id", payload.get("peer_id", "")))
	if owner_peer_id.is_empty():
		return
	var owner_username := str(payload.get("owner_username", payload.get("username", "其他成员")))
	var notice_key := owner_peer_id + "|" + rel_path
	if _scene_notice_cache.has(notice_key):
		return
	_scene_notice_cache[notice_key] = true
	_show_takeover_prompt(rel_path, owner_peer_id, owner_username, notice_key)

func _show_takeover_prompt(rel_path: String, owner_peer_id: String, owner_username: String, notice_key: String) -> void:
	var dialog := ConfirmationDialog.new()
	dialog.title = "DiffFlow - 场景被占用"
	dialog.dialog_text = "%s 正在编辑场景 \"%s\"。\n\n确定：请求接管，当前场景会先关闭；对方许可后会自动打开最新版。\n取消：放弃编辑并关闭当前场景。" % [owner_username, rel_path.get_file()]
	dialog.confirmed.connect(func() -> void:
		_scene_notice_cache.erase(notice_key)
		_request_scene_takeover(rel_path, owner_peer_id, owner_username)
		dialog.queue_free()
	)
	dialog.canceled.connect(func() -> void:
		_scene_notice_cache.erase(notice_key)
		_close_scene_path(rel_path)
		dialog.queue_free()
	)
	EditorInterface.get_base_control().add_child(dialog)
	dialog.popup_centered(Vector2i(520, 220))

func _request_scene_takeover(rel_path: String, owner_peer_id: String, owner_username: String) -> void:
	_takeover_waiting[rel_path] = {
		"owner_peer_id": owner_peer_id,
		"owner_username": owner_username,
	}
	_sync_engine.send_scene_takeover_request(rel_path, owner_peer_id)
	_close_scene_path(rel_path)
	_show_info("已向 %s 请求接管 \"%s\"。对方许可后会自动打开最新版。" % [owner_username, rel_path.get_file()])

func _on_remote_scene_takeover_requested(payload: Dictionary) -> void:
	var rel_path: String = payload.get("path", "")
	var requester_peer_id := str(payload.get("requester_peer_id", payload.get("peer_id", "")))
	if rel_path.is_empty() or requester_peer_id.is_empty():
		return
	var requester_username := str(payload.get("requester_username", payload.get("username", "其他成员")))
	if not _is_open_scene_rel_path(rel_path):
		_sync_engine.send_scene_takeover_approved(rel_path, requester_peer_id)
		return
	if _takeover_granting.has(rel_path):
		_sync_engine.send_scene_takeover_denied(rel_path, requester_peer_id, "对方正在处理另一个接管请求")
		return

	var dialog := ConfirmationDialog.new()
	dialog.title = "DiffFlow - 接管请求"
	dialog.dialog_text = "%s 请求编辑场景 \"%s\"。\n\n确定：保存、上传并关闭当前场景，让对方从最新版开始编辑。\n取消：拒绝请求。" % [requester_username, rel_path.get_file()]
	dialog.confirmed.connect(func() -> void:
		dialog.queue_free()
		call_deferred("_approve_scene_takeover", rel_path, requester_peer_id, requester_username)
	)
	dialog.canceled.connect(func() -> void:
		_sync_engine.send_scene_takeover_denied(rel_path, requester_peer_id, "对方拒绝了接管请求")
		dialog.queue_free()
	)
	EditorInterface.get_base_control().add_child(dialog)
	dialog.popup_centered(Vector2i(520, 220))

func _approve_scene_takeover(rel_path: String, requester_peer_id: String, requester_username: String) -> void:
	if _takeover_granting.has(rel_path):
		return
	_takeover_granting[rel_path] = true

	var activated: bool = await _activate_scene_for_takeover(rel_path)
	if not activated:
		var activate_message := "无法切换到目标场景保存"
		_sync_engine.send_scene_takeover_denied(rel_path, requester_peer_id, activate_message)
		_takeover_granting.erase(rel_path)
		_show_info("无法移交 \"%s\"：%s" % [rel_path.get_file(), activate_message])
		return

	var save_err := _save_active_scene(rel_path)
	if save_err != OK:
		var message := "保存当前场景失败：%d" % save_err
		_sync_engine.send_scene_takeover_denied(rel_path, requester_peer_id, message)
		_takeover_granting.erase(rel_path)
		_show_info("无法移交 \"%s\"：%s" % [rel_path.get_file(), message])
		return

	var upload_result: Dictionary = await _upload_local_file_and_wait(rel_path)
	if not bool(upload_result.get("ok", false)):
		var error := str(upload_result.get("error", "上传失败"))
		_sync_engine.send_scene_takeover_denied(rel_path, requester_peer_id, error)
		_takeover_granting.erase(rel_path)
		_show_info("无法移交 \"%s\"：%s" % [rel_path.get_file(), error])
		return

	_close_scene_path(rel_path)
	_sync_engine.send_scene_takeover_approved(rel_path, requester_peer_id, str(upload_result.get("sha", "")))
	_takeover_granting.erase(rel_path)
	_show_info("已保存并移交 \"%s\" 给 %s。" % [rel_path.get_file(), requester_username])

func _on_remote_scene_takeover_approved(payload: Dictionary) -> void:
	var rel_path: String = payload.get("path", "")
	if rel_path.is_empty():
		return
	call_deferred("_open_latest_scene_after_takeover", rel_path, str(payload.get("sha256", "")))

func _on_remote_scene_takeover_denied(payload: Dictionary) -> void:
	var rel_path: String = payload.get("path", "")
	if rel_path.is_empty():
		return
	_takeover_waiting.erase(rel_path)
	_close_scene_path(rel_path)
	var reason := str(payload.get("reason", "对方拒绝了接管请求"))
	_show_info("接管 \"%s\" 未完成：%s" % [rel_path.get_file(), reason])

func _is_open_scene(res_path: String) -> bool:
	if not (res_path.ends_with(".tscn") or res_path.ends_with(".scn")):
		return false
	var open_scenes := EditorInterface.get_open_scenes()
	return res_path in open_scenes

func _close_scene() -> void:
	var rel_path := _get_active_scene_rel_path()
	if rel_path.is_empty():
		EditorInterface.close_scene()
	else:
		_close_scene_path(rel_path)

func _close_scene_path(rel_path: String) -> void:
	if rel_path.is_empty() or not _is_open_scene_rel_path(rel_path):
		return
	if not _is_active_scene(rel_path):
		EditorInterface.open_scene_from_path("res://" + rel_path)
	EditorInterface.close_scene()
	if _sync_engine and _sync_engine.is_server_connected():
		_sync_engine.send_scene_released(rel_path)
	_open_scene_cache.erase(rel_path)

func _broadcast_open_scenes_if_changed(force: bool = false) -> void:
	if not _sync_engine or not _sync_engine.is_server_connected():
		return
	var current_scenes := _get_open_scene_rel_paths()
	for rel_path in _open_scene_cache.keys():
		if not current_scenes.has(rel_path):
			_sync_engine.send_scene_released(str(rel_path))
	for rel_path in current_scenes.keys():
		if force or not _open_scene_cache.has(rel_path):
			_sync_engine.send_scene_opened(str(rel_path))
	_open_scene_cache = current_scenes

func _get_active_scene_rel_path() -> String:
	var root := EditorInterface.get_edited_scene_root()
	if root == null:
		return ""
	var scene_path := root.scene_file_path.replace("\\", "/")
	if scene_path.is_empty():
		return ""
	return _scene_path_to_rel_path(scene_path)

func _get_open_scene_rel_paths() -> Dictionary:
	var result: Dictionary = {}
	for res_path_value in EditorInterface.get_open_scenes():
		var rel_path := _scene_path_to_rel_path(str(res_path_value))
		if not rel_path.is_empty():
			result[rel_path] = true
	return result

func _scene_path_to_rel_path(scene_path: String) -> String:
	var normalized := scene_path.replace("\\", "/")
	if not (normalized.ends_with(".tscn") or normalized.ends_with(".scn")):
		return ""
	if normalized.begins_with("res://"):
		return normalized.substr("res://".length())
	var localized := ProjectSettings.localize_path(normalized).replace("\\", "/")
	if localized.begins_with("res://"):
		return localized.substr("res://".length())
	return ""

func _is_active_scene(rel_path: String) -> bool:
	return _get_active_scene_rel_path() == rel_path

func _is_open_scene_rel_path(rel_path: String) -> bool:
	return _is_open_scene("res://" + rel_path)

func _activate_scene_for_takeover(rel_path: String) -> bool:
	if _is_active_scene(rel_path):
		return true
	if not _is_open_scene_rel_path(rel_path):
		return false
	EditorInterface.open_scene_from_path("res://" + rel_path)
	await get_tree().process_frame
	return _is_active_scene(rel_path)

func _save_active_scene(rel_path: String) -> int:
	if not _is_active_scene(rel_path):
		return ERR_DOES_NOT_EXIST
	if not EditorInterface.has_method("save_scene"):
		return ERR_UNAVAILABLE
	var result: Variant = EditorInterface.save_scene()
	if typeof(result) == TYPE_INT:
		return int(result)
	return OK

func _upload_local_file_and_wait(rel_path: String) -> Dictionary:
	_file_watcher.upload_local_file(rel_path)
	while true:
		var result: Array = await _file_watcher.upload_finished
		if result.size() >= 4 and str(result[0]) == rel_path:
			return {
				"ok": bool(result[1]),
				"sha": str(result[2]),
				"error": str(result[3]),
			}
	return {"ok": false, "sha": "", "error": "上传状态未知"}

func _open_latest_scene_after_takeover(rel_path: String, sha256: String) -> void:
	_file_watcher.apply_remote_update({"path": rel_path, "sha256": sha256})
	while true:
		var result: Array = await _file_watcher.download_finished
		if result.size() >= 4 and str(result[0]) == rel_path:
			_takeover_waiting.erase(rel_path)
			if bool(result[1]):
				EditorInterface.open_scene_from_path("res://" + rel_path)
				call_deferred("_broadcast_open_scenes_if_changed", true)
			else:
				_show_info("接管已许可，但下载最新版失败：%s" % str(result[3]))
			return

func _ask_merge(rel_path: String, payload: Dictionary) -> void:
	var dialog := ConfirmationDialog.new()
	dialog.title = "DiffFlow - 远程冲突"
	dialog.dialog_text = "场景 \"%s\" 已由他人更新。\n\n确定：接收远程版本\n取消：保留本地版本并重新上传" % rel_path.get_file()
	
	dialog.confirmed.connect(func() -> void:
		_file_watcher.apply_remote_update(payload)
		dialog.queue_free()
	)
	dialog.canceled.connect(func() -> void:
		_file_watcher.upload_local_file(rel_path, str(payload.get("sha256", "")))
		dialog.queue_free()
	)
	
	EditorInterface.get_base_control().add_child(dialog)
	dialog.popup_centered(Vector2i(450, 180))

func _on_sync_conflict(rel_path: String, action: String, remote_sha: String) -> void:
	var dialog := ConfirmationDialog.new()
	dialog.title = "DiffFlow - 保存冲突"
	var action_text := "上传" if action == "upload" else "删除"
	dialog.dialog_text = "文件 \"%s\" 在远端已有新版本，当前%s已被拦截。\n\n确定：接收远端版本\n取消：保留本地并覆盖远端" % [rel_path.get_file(), action_text]

	dialog.confirmed.connect(func() -> void:
		_accept_remote_after_conflict(rel_path, remote_sha)
		dialog.queue_free()
	)
	dialog.canceled.connect(func() -> void:
		if action == "delete":
			_file_watcher.delete_remote_file(rel_path, remote_sha)
		else:
			_file_watcher.upload_local_file(rel_path, remote_sha)
		dialog.queue_free()
	)

	EditorInterface.get_base_control().add_child(dialog)
	dialog.popup_centered(Vector2i(460, 190))

func _accept_remote_after_conflict(rel_path: String, remote_sha: String) -> void:
	if remote_sha.is_empty():
		_file_watcher.apply_remote_delete({"path": rel_path})
	else:
		_file_watcher.apply_remote_update({"path": rel_path, "sha256": remote_sha})

func _show_info(text: String) -> void:
	var dialog := AcceptDialog.new()
	dialog.title = "DiffFlow"
	dialog.dialog_text = text
	dialog.confirmed.connect(dialog.queue_free)
	EditorInterface.get_base_control().add_child(dialog)
	dialog.popup_centered(Vector2i(400, 150))
