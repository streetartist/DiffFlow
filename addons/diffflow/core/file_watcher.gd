@tool
extends Node
class_name DFFileWatcher

## Watches project files and syncs bytes through the server HTTP API.

signal sync_conflict(rel_path: String, action: String, remote_sha: String)
signal upload_finished(rel_path: String, ok: bool, sha: String, error: String)
signal download_finished(rel_path: String, ok: bool, sha: String, error: String)

var sync_engine: DFSyncEngine
var plugin  # EditorPlugin 引用

var _watching := false
var _poll_timer := 0.0
const POLL_INTERVAL := 1.5

var _file_cache: Dictionary = {}  # { rel_path: mtime }
var _hash_cache: Dictionary = {}  # { rel_path: local sha256 }
var _base_sha_cache: Dictionary = {}  # { rel_path: last known server sha256 }
var _project_path: String = ""
var _server_url: String = ""
var _token: String = ""
var _project_id: int = 0
var _max_file_bytes: int = 100 * 1024 * 1024
var _initial_syncing := false
var _transferring: Dictionary = {}
var _ignore_rules: Array = []
var _ignore_file_mtime: int = -1

const IGNORE_FILE_NAME := ".diffflowignore"
const BUILTIN_IGNORE_DIRS := [".git", ".godot"]

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
	_base_sha_cache.clear()
	_load_ignore_rules(true)
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
	_load_ignore_rules()
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
		var base_sha: String = str(_base_sha_cache.get(rel_path, ""))
		if _should_ignore_path(rel_path):
			_base_sha_cache.erase(rel_path)
			continue
		_delete_remote(rel_path, base_sha)

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

	if server_files.has(IGNORE_FILE_NAME):
		await _sync_manifest_file(IGNORE_FILE_NAME, server_files[IGNORE_FILE_NAME])
		_load_ignore_rules(true)

	for rel_path_value in server_files.keys():
		var rel_path: String = str(rel_path_value)
		if rel_path == IGNORE_FILE_NAME:
			continue
		if _should_ignore_path(rel_path):
			continue
		var remote: Dictionary = server_files[rel_path]
		await _sync_manifest_file(rel_path, remote)

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

func _sync_manifest_file(rel_path: String, remote: Dictionary) -> void:
	var full_path: String = _project_path + rel_path
	if not FileAccess.file_exists(full_path):
		await _download_file(rel_path, str(remote.get("sha256", "")))
		return

	var local_hash: String = _file_sha256(full_path)
	_hash_cache[rel_path] = local_hash
	if local_hash == remote.get("sha256", ""):
		_base_sha_cache[rel_path] = local_hash
		return

	var local_mtime: int = FileAccess.get_modified_time(full_path)
	var remote_mtime: int = int(remote.get("mtime", 0))
	if local_mtime > remote_mtime:
		await _upload_file(rel_path, str(remote.get("sha256", "")))
	else:
		await _download_file(rel_path, str(remote.get("sha256", "")))

func _to_rel_path(full_path: String) -> String:
	var normalized: String = full_path.replace("\\", "/")
	if normalized.begins_with(_project_path):
		return normalized.substr(_project_path.length())
	return normalized

func _normalize_rel_path(rel_path: String) -> String:
	var normalized: String = rel_path.replace("\\", "/").strip_edges()
	while normalized.begins_with("./"):
		normalized = normalized.substr(2)
	while normalized.begins_with("/"):
		normalized = normalized.substr(1)
	return normalized

func _load_ignore_rules(force: bool = false) -> void:
	var ignore_path: String = _project_path + IGNORE_FILE_NAME
	var mtime: int = -1
	if FileAccess.file_exists(ignore_path):
		mtime = FileAccess.get_modified_time(ignore_path)
	if not force and mtime == _ignore_file_mtime:
		return

	_ignore_file_mtime = mtime
	_ignore_rules.clear()
	if mtime < 0:
		return

	var file: FileAccess = FileAccess.open(ignore_path, FileAccess.READ)
	if file == null:
		return

	while not file.eof_reached():
		var line: String = file.get_line().strip_edges()
		if line.is_empty() or line.begins_with("#"):
			continue

		var negated := false
		if line.begins_with("!"):
			negated = true
			line = line.substr(1).strip_edges()
			if line.is_empty():
				continue

		line = line.replace("\\", "/").strip_edges()
		while line.begins_with("./"):
			line = line.substr(2)
		var anchored := false
		if line.begins_with("/"):
			anchored = true
			line = line.substr(1)

		var dir_only := false
		while line.ends_with("/") and not line.is_empty():
			dir_only = true
			line = line.substr(0, line.length() - 1)
		if line.is_empty():
			continue

		_ignore_rules.append({
			"pattern": line,
			"negated": negated,
			"dir_only": dir_only,
			"anchored": anchored,
		})
	file.close()

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
			if not _should_ignore_dir(rel_path):
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

func _should_ignore_dir(rel_path: String) -> bool:
	rel_path = _normalize_rel_path(rel_path)
	if _is_builtin_ignored(rel_path):
		return true
	return _matches_ignore_rules(rel_path, true)

func _should_ignore_path(rel_path: String) -> bool:
	rel_path = _normalize_rel_path(rel_path)
	if rel_path == IGNORE_FILE_NAME:
		return false
	if _is_builtin_ignored(rel_path):
		return true
	return _matches_ignore_rules(rel_path, false)

func _is_builtin_ignored(rel_path: String) -> bool:
	var parts: PackedStringArray = rel_path.split("/", false)
	for part in parts:
		for dirname in BUILTIN_IGNORE_DIRS:
			if part == dirname:
				return true
	return false

func _matches_ignore_rules(rel_path: String, is_dir: bool) -> bool:
	var ignored := false
	for rule in _ignore_rules:
		if _ignore_rule_matches(rule, rel_path, is_dir):
			var negated: bool = rule.get("negated", false)
			ignored = not negated
	return ignored

func _ignore_rule_matches(rule: Dictionary, rel_path: String, is_dir: bool) -> bool:
	var pattern: String = rule.get("pattern", "")
	if pattern.is_empty():
		return false

	var dir_only: bool = rule.get("dir_only", false)
	var anchored: bool = rule.get("anchored", false)
	if dir_only:
		for candidate in _dir_candidates(rel_path, is_dir):
			if _pattern_matches_path(pattern, str(candidate), anchored):
				return true
		return false
	return _pattern_matches_path(pattern, rel_path, anchored)

func _dir_candidates(rel_path: String, is_dir: bool) -> Array:
	var candidates: Array = []
	var path := rel_path
	if not is_dir:
		path = rel_path.get_base_dir()
	if path == "." or path.is_empty():
		return candidates

	var parts: PackedStringArray = path.split("/", false)
	var current := ""
	for part in parts:
		current = part if current.is_empty() else current + "/" + part
		candidates.append(current)
	return candidates

func _pattern_matches_path(pattern: String, rel_path: String, anchored: bool) -> bool:
	var has_slash := pattern.find("/") >= 0
	if anchored or has_slash:
		return rel_path.match(pattern)

	var parts: PackedStringArray = rel_path.split("/", false)
	for part in parts:
		if part.match(pattern):
			return true
	return rel_path.get_file().match(pattern)

func upload_local_file(rel_path: String, base_sha_override: Variant = null) -> void:
	call_deferred("_upload_file", rel_path, base_sha_override)

func _upload_file(rel_path: String, base_sha_override: Variant = null) -> void:
	if _server_url.is_empty() or _token.is_empty() or _project_id <= 0:
		upload_finished.emit(rel_path, false, "", "not configured")
		return
	if _should_ignore_path(rel_path):
		upload_finished.emit(rel_path, false, "", "ignored")
		return
	var full_path: String = _project_path + rel_path
	if not FileAccess.file_exists(full_path):
		upload_finished.emit(rel_path, false, "", "file not found")
		return
	var size: int = _file_size(full_path)
	if size > _max_file_bytes:
		print("[DiffFlow] 跳过超过同步阈值的文件：", rel_path, " (", size, " bytes)")
		upload_finished.emit(rel_path, false, "", "file exceeds sync threshold")
		return
	var file: FileAccess = FileAccess.open(full_path, FileAccess.READ)
	if file == null:
		upload_finished.emit(rel_path, false, "", "could not open file")
		return
	var content: PackedByteArray = file.get_buffer(file.get_length())
	file.close()
	var mtime: int = FileAccess.get_modified_time(full_path)
	var base_sha: String = str(_base_sha_cache.get(rel_path, ""))
	if base_sha_override != null:
		base_sha = str(base_sha_override)
	_transferring[rel_path] = true
	var path: String = "/api/projects/%d/files?path=%s&mtime=%d&base_sha=%s" % [_project_id, rel_path.uri_encode(), mtime, base_sha.uri_encode()]
	var response: Dictionary = await _request_raw(HTTPClient.METHOD_PUT, path, content)
	_transferring.erase(rel_path)
	if response.get("ok", false):
		_file_cache[rel_path] = mtime
		var data: Dictionary = response.get("data", {})
		var sha: String = data.get("sha256", _file_sha256(full_path))
		_hash_cache[rel_path] = sha
		_base_sha_cache[rel_path] = sha
		upload_finished.emit(rel_path, true, sha, "")
	elif int(response.get("status", 0)) == 409:
		sync_conflict.emit(rel_path, "upload", _conflict_remote_sha(response))
		upload_finished.emit(rel_path, false, "", "conflict")
	else:
		var error := str(response.get("error", "unknown error"))
		push_warning("[DiffFlow] 上传失败：" + rel_path + " - " + error)
		upload_finished.emit(rel_path, false, "", error)

func delete_remote_file(rel_path: String, base_sha_override: Variant = null) -> void:
	call_deferred("_delete_remote", rel_path, base_sha_override)

func _delete_remote(rel_path: String, base_sha_override: Variant = null) -> void:
	if _server_url.is_empty() or _token.is_empty() or _project_id <= 0:
		return
	if _should_ignore_path(rel_path):
		return
	var base_sha: String = str(_base_sha_cache.get(rel_path, ""))
	if base_sha_override != null:
		base_sha = str(base_sha_override)
	var mtime: int = int(Time.get_unix_time_from_system())
	var path: String = "/api/projects/%d/files?path=%s&mtime=%d&base_sha=%s" % [_project_id, rel_path.uri_encode(), mtime, base_sha.uri_encode()]
	var response: Dictionary = await _request_json(HTTPClient.METHOD_DELETE, path)
	if response.get("ok", false):
		_base_sha_cache.erase(rel_path)
	elif int(response.get("status", 0)) == 409:
		sync_conflict.emit(rel_path, "delete", _conflict_remote_sha(response))
	else:
		push_warning("[DiffFlow] 删除同步失败：" + rel_path + " - " + str(response.get("error", "unknown error")))

func apply_remote_update(payload: Dictionary) -> void:
	var rel_path: String = payload.get("path", "")
	if rel_path.is_empty() or _should_ignore_path(rel_path):
		return
	call_deferred("_download_file", rel_path, str(payload.get("sha256", "")))

func apply_remote_delete(payload: Dictionary) -> void:
	var rel_path: String = payload.get("path", "")
	if rel_path.is_empty() or _should_ignore_path(rel_path):
		return
	var full_path: String = _project_path + rel_path
	var res_path: String = "res://" + rel_path

	_file_cache.erase(rel_path)
	_hash_cache.erase(rel_path)
	_base_sha_cache.erase(rel_path)
	if FileAccess.file_exists(full_path):
		_transferring[rel_path] = true
		DirAccess.remove_absolute(full_path)
		_transferring.erase(rel_path)
		EditorInterface.get_resource_filesystem().update_file(res_path)

func _download_file(rel_path: String, expected_sha: String = "") -> void:
	if _server_url.is_empty() or _token.is_empty() or _project_id <= 0:
		download_finished.emit(rel_path, false, "", "not configured")
		return
	if _should_ignore_path(rel_path):
		download_finished.emit(rel_path, false, "", "ignored")
		return
	var path: String = "/api/projects/%d/files?path=%s" % [_project_id, rel_path.uri_encode()]
	_transferring[rel_path] = true
	var response: Dictionary = await _request_raw(HTTPClient.METHOD_GET, path)
	if not response.get("ok", false):
		_transferring.erase(rel_path)
		var error := str(response.get("error", "unknown error"))
		push_warning("[DiffFlow] 下载失败：" + rel_path + " - " + error)
		download_finished.emit(rel_path, false, "", error)
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
		var sha := _file_sha256(full_path)
		_hash_cache[rel_path] = sha
		_base_sha_cache[rel_path] = expected_sha if not expected_sha.is_empty() else sha
		if rel_path == IGNORE_FILE_NAME:
			_load_ignore_rules(true)
		_reload_in_editor(res_path)
		download_finished.emit(rel_path, true, sha, "")
	else:
		download_finished.emit(rel_path, false, "", "could not write file")
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

func _conflict_remote_sha(response: Dictionary) -> String:
	var body: PackedByteArray = response.get("body", PackedByteArray())
	var parsed: Variant = JSON.parse_string(body.get_string_from_utf8())
	if parsed is Dictionary:
		return str(parsed.get("current_sha", ""))
	return ""

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
