@tool
extends VBoxContainer

## Visual diff viewer for showing git diffs of files.

@onready var file_list: ItemList = $FileList
@onready var diff_display: RichTextLabel = $DiffDisplay

var _changed_files: PackedStringArray = []

func _ready() -> void:
	file_list.item_selected.connect(_on_file_selected)
	$HeaderBar/RefreshBtn.pressed.connect(refresh)

func refresh() -> void:
	file_list.clear()
	_changed_files.clear()

	var result := DFGitManager.status()
	if result.exit_code != 0:
		return

	var lines: PackedStringArray = result.output.split("\n")
	for line in lines:
		if line.strip_edges().is_empty():
			continue
		var fpath := line.substr(3).strip_edges()
		_changed_files.append(fpath)
		file_list.add_item(fpath)

func _on_file_selected(idx: int) -> void:
	if idx < 0 or idx >= _changed_files.size():
		return

	var fpath := _changed_files[idx]
	var result := DFGitManager.diff(fpath)

	diff_display.clear()
	diff_display.push_font_size(12)

	if result.output.is_empty():
		diff_display.add_text("无差异（新文件或二进制文件）")
		diff_display.pop()
		return

	var lines: PackedStringArray = result.output.split("\n")
	for line in lines:
		if line.begins_with("+++") or line.begins_with("---"):
			diff_display.push_color(Color(0.6, 0.6, 1.0))
			diff_display.add_text(line)
			diff_display.pop()
		elif line.begins_with("@@"):
			diff_display.push_color(Color(0.8, 0.5, 1.0))
			diff_display.add_text(line)
			diff_display.pop()
		elif line.begins_with("+"):
			diff_display.push_color(Color(0.4, 1.0, 0.4))
			diff_display.add_text(line)
			diff_display.pop()
		elif line.begins_with("-"):
			diff_display.push_color(Color(1.0, 0.4, 0.4))
			diff_display.add_text(line)
			diff_display.pop()
		else:
			diff_display.add_text(line)
		diff_display.add_text("\n")

	diff_display.pop()
