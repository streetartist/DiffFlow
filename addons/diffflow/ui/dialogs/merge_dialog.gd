@tool
extends ConfirmationDialog
class_name DFMergeDialog

## Dialog for merging branches.

signal merge_confirmed(source_branch: String)

var _branch_option: OptionButton
var _info_label: Label

func _ready() -> void:
	title = "Merge Branch"
	size = Vector2i(400, 200)

	var vbox := VBoxContainer.new()
	add_child(vbox)

	vbox.add_child(_make_label("Merge from:"))
	_branch_option = OptionButton.new()
	vbox.add_child(_branch_option)

	_info_label = Label.new()
	_info_label.text = "This will merge the selected branch into the current branch."
	_info_label.autowrap_mode = TextServer.AUTOWRAP_WORD_SMART
	vbox.add_child(_info_label)

	confirmed.connect(_on_confirmed)
	_load_branches()

func _make_label(text: String) -> Label:
	var l := Label.new()
	l.text = text
	return l

func _load_branches() -> void:
	var result := DFGitManager.branch_list()
	if result.exit_code != 0:
		return
	var lines: PackedStringArray = result.output.split("\n")
	for line in lines:
		var branch_name := line.strip_edges()
		if branch_name.is_empty() or branch_name.begins_with("* "):
			continue
		_branch_option.add_item(branch_name)

func _on_confirmed() -> void:
	if _branch_option.selected < 0:
		return
	var branch := _branch_option.get_item_text(_branch_option.selected)
	merge_confirmed.emit(branch)
