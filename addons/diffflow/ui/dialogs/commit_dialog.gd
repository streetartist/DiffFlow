@tool
extends ConfirmationDialog
class_name DFCommitDialog

## Dialog for creating a git commit with a message.

signal commit_confirmed(message: String)

var _msg_edit: TextEdit
var _status_label: RichTextLabel

func _ready() -> void:
	title = "Commit Changes"
	size = Vector2i(500, 350)

	var vbox := VBoxContainer.new()
	add_child(vbox)

	vbox.add_child(_make_label("Changed Files:"))
	_status_label = RichTextLabel.new()
	_status_label.custom_minimum_size = Vector2(0, 100)
	_status_label.bbcode_enabled = true
	vbox.add_child(_status_label)

	vbox.add_child(HSeparator.new())
	vbox.add_child(_make_label("Commit Message:"))
	_msg_edit = TextEdit.new()
	_msg_edit.custom_minimum_size = Vector2(0, 80)
	_msg_edit.placeholder_text = "Describe your changes..."
	vbox.add_child(_msg_edit)

	confirmed.connect(_on_confirmed)
	_load_status()

func _make_label(text: String) -> Label:
	var l := Label.new()
	l.text = text
	return l

func _load_status() -> void:
	var result := DFGitManager.status()
	if result.exit_code == 0:
		var lines: PackedStringArray = result.output.split("\n")
		var bbcode := ""
		for line in lines:
			if line.strip_edges().is_empty():
				continue
			if line.begins_with(" M") or line.begins_with("M"):
				bbcode += "[color=yellow]" + line.strip_edges() + "[/color]\n"
			elif line.begins_with("??"):
				bbcode += "[color=green]" + line.strip_edges() + "[/color]\n"
			elif line.begins_with(" D") or line.begins_with("D"):
				bbcode += "[color=red]" + line.strip_edges() + "[/color]\n"
			else:
				bbcode += line.strip_edges() + "\n"
		_status_label.text = bbcode
	else:
		_status_label.text = "Could not read git status."

func _on_confirmed() -> void:
	var msg := _msg_edit.text.strip_edges()
	if msg.is_empty():
		return
	commit_confirmed.emit(msg)
