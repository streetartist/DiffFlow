@tool
extends Node
class_name DFFileWatcher

## Watches project files and syncs bytes through the server HTTP API.

var sync_engine: DFSyncEngine
var plugin  # EditorPlugin 引用

var _watching := false
var _poll_timer := 0.0
const POLL_INTERVAL := 1.5

var _file_cache: Dictionary = {}  # { rel_path: mtime }
var _hash_cache: Dictionary = {}  # { rel_path: sha256 }
var _project_path: String = ""
var _server_url: String = ""
var _token: String = ""
var _project_id: int = 0
var _max_file_bytes: int = 100 * 1024 * 1024
var _initial_syncing := false
var _transferring: Dictionary = {}

const IGNORE_DIRS := [".godot", ".git", ".import", ".claude", ".uid"]
const IGNORE_FILE_SUFFIXES := [".uid", ".import", ".tmp", ".remap", ".db", ".sqlite", ".sqlite3", "-shm", "-wal", "-journal"]

func configure(server_url: String, token: String, project_id: int, max_file_bytes: int) -> void:
	_server_url = _normalize_server_url(server_url)
	_token = token
	_project_id = project_id
	if max_file_bytes > 0:
		_max_file_bytes = max_file_bytes

func start(project_path: String) -> void:
	_project_path = project_path.replace("\\", "/")
	if not _project_path.ends_with("/"):
		_project_path += "/"
	_file_cache.clear()
	_hash_cache.clear()
	_scan_dir(_project_path, true)
	_watching = false
	_initial_syncing = true
	print("[DiffFlow] 开始首次同步，本地文件：", _file_cache.size())
	call_deferred("_initial_sync")

func stop() -> void:
	_watching = false
	_initial_syncing = false

func _process(delta: float) -> void:
	if not _watching or _initial_syncing:
		return

	_poll_timer += delta
	if _poll_timer >= POLL_INTERVAL:
		_poll_timer = 0.0
		_check_changes()

func _check_changes() -> void:
	var current_files: Dictionary = {}
	_scan_dir(_project_path, false, current_files)

	for rel_path_value in current_files.keys():
		var rel_path: String = str(rel_path_value)
		if _transferring.has(rel_path):
			continue
		var mtime: int = current_files[rel_path]
		if not _file_cache.has(rel_path) or _file_cache[rel_path] != mtime:
			_file_cache[rel_path] = mtime
			_upload_file(rel_path)

	var to_delete: Array = []
	for rel_path_value in _file_cache.keys():
		var rel_path: String = str(rel_path_value)
		if not current_files.has(rel_path) and not _transferring.has(rel_path):
			to_delete.append(rel_path)
	for rel_path_value in to_delete:
		var rel_path: String = str(rel_path_value)
		_file_cache.erase(rel_path)
		_hash_cache.erase(rel_path)
		_delete_remote(rel_path)

func _initial_sync() -> void:
	if _server_url.is_empty() or _token.is_empty() or _project_id <= 0:
		_initial_syncing = false
		return

	var response: Dictionary = await _request_json(HTTPClient.METHOD_GET, "/api/projects/%d/manifest" % _project_id)
	if not response.get("ok", false):
		push_warning("[DiffFlow] 首次同步失败：" + str(response.get("error", "unknown error")))
		_initial_syncing = false
		return

	var data: Dictionary = response.get("data", {})
	_max_file_bytes = int(data.get("max_file_bytes", _max_file_bytes))
	var server_files: Dictionary = {}
	var files_value: Variant = data.get("files", [])
	if files_value is Array:
		for item in files_value:
			if item is Dictionary:
				server_files[item.get("path", "")] = item

	for rel_path_value in server_files.keys():
		var rel_path: String = str(rel_path_value)
		if _should_ignore_path(rel_path):
			continue
		var remote: Dictionary = server_files[rel_path]
		var full_path: String = _project_path + rel_path
		if not FileAccess.file_exists(full_path):
			await _download_file(rel_path)
			continue
		var local_hash: String = _file_sha256(full_path)
		_hash_cache[rel_path] = local_hash
		if local_hash != remote.get("sha256", ""):
			var local_mtime: int = FileAccess.get_modified_time(full_path)
			var remote_mtime: int = int(remote.get("mtime", 0))
			if local_mtime > remote_mtime:
				await _upload_file(rel_path)
			else:
				await _download_file(rel_path)

	for rel_path_value in _file_cache.keys():
		var rel_path: String = str(rel_path_value)
		if not server_files.has(rel_path):
			await _upload_file(rel_path)

	_file_cache.clear()
	_hash_cache.clear()
	_scan_dir(_project_path, true)
	_initial_syncing = false
	_watching = true
	print("[DiffFlow] 首次同步完成，开始实时监听。")

func _to_rel_path(full_path: String) -> String:
	var normalized: String = full_path.replace("\\", "/")
	if normalized.begins_with(_project_path):
		return normalized.substr(_project_path.length())
	return normalized

func _scan_dir(path: String, initial: bool, out: Dictionary = {}) -> void:
	var dir: DirAccess = DirAccess.open(path)
	if dir == null:
		return
	dir.list_dir_begin()
	var fname: String = dir.get_next()
	while fname != "":
		if fname == "." or fname == "..":
			fname = dir.get_next()
			continue
		var full_path: String = path.path_join(fname).replace("\\", "/")
		var rel_path: String = _to_rel_path(full_path)
		if dir.current_is_dir():
			if not _should_ignore_dir(fname):
				_scan_dir(full_path, initial, out)
		else:
			if not _should_ignore_path(rel_path):
				var mtime: int = FileAccess.get_modified_time(full_path)
				if initial:
					_file_cache[rel_path] = mtime
				else:
					out[rel_path] = mtime
		fname = dir.get_next()
	dir.list_dir_end()

func _should_ignore_dir(dirname: String) -> bool:
	for d in IGNORE_DIRS:
		if dirname == d or dirname.begins_with(d):
			return true
	return false

func _should_ignore_path(rel_path: String) -> bool:
	var filename: String = rel_path.get_file()
	for suffix in IGNORE_FILE_SUFFIXES:
		if filename.ends_with(suffix):
			return true
	return false

func upload_local_file(rel_path: String) -> void:
	call_deferred("_upload_file", rel_path)

func _upload_file(rel_path: String) -> void:
	if _server_url.is_empty() or _token.is_empty() or _project_id <= 0:
		return
	if _should_ignore_path(rel_path):
		return
	var full_path: String = _project_path + rel_path
	if not FileAccess.file_exists(full_path):
		return
	var size: int = _file_size(full_path)
	if size > _max_file_bytes:
		print("[DiffFlow] 跳过超过同步阈值的文件：", rel_path, " (", size, " bytes)")
		return
	var file: FileAccess = FileAccess.open(full_path, FileAccess.READ)
	if file == null:
		return
	var content: PackedByteArray = file.get_buffer(file.get_length())
	file.close()
	var mtime: int = FileAccess.get_modified_time(full_path)
	_transferring[rel_path] = true
	var path: String = "/api/projects/%d/files?path=%s&mtime=%d" % [_project_id, rel_path.uri_encode(), mtime]
	var response: Dictionary = await _request_raw(HTTPClient.METHOD_PUT, path, content)
	_transferring.erase(rel_path)
	if response.get("ok", false):
		_file_cache[rel_path] = mtime
		var data: Dictionary = response.get("data", {})
		_hash_cache[rel_path] = data.get("sha256", _file_sha256(full_path))
	else:
		push_warning("[DiffFlow] 上传失败：" + rel_path + " - " + str(response.get("error", "unknown error")))

func _delete_remote(rel_path: String) -> void:
	if _server_url.is_empty() or _token.is_empty() or _project_id <= 0:
		return
	if _should_ignore_path(rel_path):
		return
	var mtime: int = int(Time.get_unix_time_from_system())
	var path: String = "/api/projects/%d/files?path=%s&mtime=%d" % [_project_id, rel_path.uri_encode(), mtime]
	var response: Dictionary = await _request_json(HTTPClient.METHOD_DELETE, path)
	if not response.get("ok", false):
		push_warning("[DiffFlow] 删除同步失败：" + rel_path + " - " + str(response.get("error", "unknown error")))

func apply_remote_update(payload: Dictionary) -> void:
	var rel_path: String = payload.get("path", "")
	if rel_path.is_empty() or _should_ignore_path(rel_path):
		return
	call_deferred("_download_file", rel_path)

func apply_remote_delete(payload: Dictionary) -> void:
	var rel_path: String = payload.get("path", "")
	if rel_path.is_empty() or _should_ignore_path(rel_path):
		return
	var full_path: String = _project_path + rel_path
	var res_path: String = "res://" + rel_path

	if FileAccess.file_exists(full_path):
		_transferring[rel_path] = true
		DirAccess.remove_absolute(full_path)
		_file_cache.erase(rel_path)
		_hash_cache.erase(rel_path)
		_transferring.erase(rel_path)
		EditorInterface.get_resource_filesystem().update_file(res_path)

func _download_file(rel_path: String) -> void:
	if _server_url.is_empty() or _token.is_empty() or _project_id <= 0:
		return
	if _should_ignore_path(rel_path):
		return
	var path: String = "/api/projects/%d/files?path=%s" % [_project_id, rel_path.uri_encode()]
	_transferring[rel_path] = true
	var response: Dictionary = await _request_raw(HTTPClient.METHOD_GET, path)
	if not response.get("ok", false):
		_transferring.erase(rel_path)
		push_warning("[DiffFlow] 下载失败：" + rel_path + " - " + str(response.get("error", "unknown error")))
		return

	var full_path: String = _project_path + rel_path
	var res_path: String = "res://" + rel_path
	var dir_path: String = full_path.get_base_dir()
	if not DirAccess.dir_exists_absolute(dir_path):
		DirAccess.make_dir_recursive_absolute(dir_path)
	var file: FileAccess = FileAccess.open(full_path, FileAccess.WRITE)
	if file:
		var body: PackedByteArray = response.get("body", PackedByteArray())
		file.store_buffer(body)
		file.close()
		_file_cache[rel_path] = FileAccess.get_modified_time(full_path)
		_hash_cache[rel_path] = _file_sha256(full_path)
		_reload_in_editor(res_path)
	_transferring.erase(rel_path)

func _reload_in_editor(res_path: String) -> void:
	var efs: EditorFileSystem = EditorInterface.get_resource_filesystem()
	efs.update_file(res_path)

	if res_path.ends_with(".tscn") or res_path.ends_with(".scn"):
		var open_scenes: PackedStringArray = EditorInterface.get_open_scenes()
		if res_path in open_scenes:
			EditorInterface.reload_scene_from_path(res_path)
	elif res_path.ends_with(".gd") or res_path.ends_with(".cs"):
		if ResourceLoader.exists(res_path):
			var script: Resource = ResourceLoader.load(res_path, "", ResourceLoader.CACHE_MODE_REPLACE)
			if script and script is Script:
				script.reload()
	elif res_path.ends_with(".tres") or res_path.ends_with(".res"):
		if ResourceLoader.exists(res_path):
			ResourceLoader.load(res_path, "", ResourceLoader.CACHE_MODE_REPLACE)

func _request_json(method: int, path: String, body: Dictionary = {}) -> Dictionary:
	var payload: PackedByteArray = PackedByteArray()
	var headers: PackedStringArray = _auth_headers()
	if not body.is_empty():
		headers.append("Content-Type: application/json")
		payload = JSON.stringify(body).to_utf8_buffer()
	var response: Dictionary = await _request(method, path, headers, payload)
	if not response.get("ok", false):
		return response
	var text: String = response.get("body", PackedByteArray()).get_string_from_utf8()
	var parsed: Variant = JSON.parse_string(text)
	if parsed == null:
		return {"ok": false, "error": "invalid json response"}
	response["data"] = parsed
	return response

func _request_raw(method: int, path: String, body: PackedByteArray = PackedByteArray()) -> Dictionary:
	return await _request(method, path, _auth_headers(), body)

func _request(method: int, path: String, headers: PackedStringArray, body: PackedByteArray) -> Dictionary:
	var http: HTTPRequest = HTTPRequest.new()
	add_child(http)
	var url: String = _server_url + path
	var err: int
	if body.size() > 0:
		err = http.request_raw(url, headers, method, body)
	else:
		err = http.request(url, headers, method)
	if err != OK:
		http.queue_free()
		return {"ok": false, "error": "request failed: %d" % err}
	var completed: Array = await http.request_completed
	http.queue_free()
	var result: int = completed[0]
	var code: int = completed[1]
	var response_body: PackedByteArray = completed[3]
	if result != HTTPRequest.RESULT_SUCCESS:
		return {"ok": false, "status": code, "error": "network error: %d" % result, "body": response_body}
	if code < 200 or code >= 300:
		var message: String = response_body.get_string_from_utf8()
		var parsed: Variant = JSON.parse_string(message)
		if parsed is Dictionary and parsed.has("error"):
			message = parsed.get("error", message)
		return {"ok": false, "status": code, "error": message, "body": response_body}
	return {"ok": true, "status": code, "body": response_body}

func _auth_headers() -> PackedStringArray:
	var headers: PackedStringArray = PackedStringArray()
	headers.append("Authorization: Bearer " + _token)
	if sync_engine:
		headers.append("X-DiffFlow-Peer: " + sync_engine.get_peer_id())
	return headers

func _file_sha256(full_path: String) -> String:
	var file: FileAccess = FileAccess.open(full_path, FileAccess.READ)
	if file == null:
		return ""
	var ctx: HashingContext = HashingContext.new()
	ctx.start(HashingContext.HASH_SHA256)
	while not file.eof_reached():
		var chunk: PackedByteArray = file.get_buffer(1024 * 1024)
		if chunk.size() > 0:
			ctx.update(chunk)
	file.close()
	return ctx.finish().hex_encode()

func _file_size(full_path: String) -> int:
	var file: FileAccess = FileAccess.open(full_path, FileAccess.READ)
	if file == null:
		return 0
	var size: int = file.get_length()
	file.close()
	return size

func _normalize_server_url(url: String) -> String:
	url = url.strip_edges()
	while url.ends_with("/"):
		url = url.substr(0, url.length() - 1)
	return url
