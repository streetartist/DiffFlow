@tool
extends EditorPlugin

## DiffFlow: 实时协同编辑 + Git 版本管理。

var main_dock: Control
var _sync_engine: DFSyncEngine
var _file_watcher: DFFileWatcher

func _enter_tree() -> void:
	_sync_engine = DFSyncEngine.new()
	_sync_engine.name = "DFSyncEngine"
	add_child(_sync_engine)

	_file_watcher = DFFileWatcher.new()
	_file_watcher.name = "DFFileWatcher"
	_file_watcher.sync_engine = _sync_engine
	_file_watcher.plugin = self
	add_child(_file_watcher)

	_sync_engine.remote_file_updated.connect(_on_remote_file_updated)
	_sync_engine.remote_file_deleted.connect(_on_remote_file_deleted)
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
	else:
		_file_watcher.stop()

func _on_remote_file_updated(payload: Dictionary) -> void:
	var rel_path: String = payload.get("path", "")
	var res_path: String = "res://" + rel_path
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

func _is_open_scene(res_path: String) -> bool:
	if not (res_path.ends_with(".tscn") or res_path.ends_with(".scn")):
		return false
	var open_scenes := EditorInterface.get_open_scenes()
	return res_path in open_scenes

func _close_scene() -> void:
	EditorInterface.close_scene()

func _ask_merge(rel_path: String, payload: Dictionary) -> void:
	var dialog := ConfirmationDialog.new()
	dialog.title = "DiffFlow - 远程冲突"
	dialog.dialog_text = "场景 \"%s\" 已由他人更新。\n\n确定：接收远程版本\n取消：保留本地版本并重新上传" % rel_path.get_file()
	
	dialog.confirmed.connect(func() -> void:
		_file_watcher.apply_remote_update(payload)
		dialog.queue_free()
	)
	dialog.canceled.connect(func() -> void:
		_file_watcher.upload_local_file(rel_path)
		dialog.queue_free()
	)
	
	EditorInterface.get_base_control().add_child(dialog)
	dialog.popup_centered(Vector2i(450, 180))

func _show_info(text: String) -> void:
	var dialog := AcceptDialog.new()
	dialog.title = "DiffFlow"
	dialog.dialog_text = text
	dialog.confirmed.connect(dialog.queue_free)
	EditorInterface.get_base_control().add_child(dialog)
	dialog.popup_centered(Vector2i(400, 150))
