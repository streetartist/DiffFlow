@tool
extends VBoxContainer

## Presence overlay showing other users' selections in the scene tree.

var sync_engine  # DFSyncEngine (GDExtension)
var _peer_colors: Dictionary = {}
var _color_palette: Array[Color] = [
	Color(0.2, 0.6, 1.0, 0.3),
	Color(1.0, 0.4, 0.2, 0.3),
	Color(0.2, 1.0, 0.4, 0.3),
	Color(1.0, 0.2, 0.8, 0.3),
	Color(1.0, 1.0, 0.2, 0.3),
	Color(0.6, 0.2, 1.0, 0.3),
]
var _next_color_idx: int = 0

func initialize(engine) -> void:
	sync_engine = engine
	if sync_engine:
		sync_engine.presence_updated.connect(_on_presence_updated)
		sync_engine.peer_left.connect(_on_peer_left)

func _on_presence_updated(peer_info: Dictionary) -> void:
	var peer_id: String = peer_info.get("peer_id", "")
	if peer_id.is_empty():
		return
	if not _peer_colors.has(peer_id):
		_peer_colors[peer_id] = _color_palette[_next_color_idx % _color_palette.size()]
		_next_color_idx += 1
	queue_redraw()

func _on_peer_left(peer_id: String) -> void:
	_peer_colors.erase(peer_id)
	queue_redraw()

func get_peer_color(peer_id: String) -> Color:
	if _peer_colors.has(peer_id):
		return _peer_colors[peer_id]
	return Color.WHITE
