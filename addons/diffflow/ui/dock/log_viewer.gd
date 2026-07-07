@tool
extends VBoxContainer

## Git log viewer showing recent commit history.

@onready var log_list: ItemList = $LogList
@onready var detail_label: RichTextLabel = $DetailLabel

func _ready() -> void:
	$HeaderBar/RefreshBtn.pressed.connect(refresh)
	log_list.item_selected.connect(_on_log_selected)

func refresh() -> void:
	log_list.clear()
	var result := DFGitManager.log_short(30)
	if result.exit_code != 0:
		log_list.add_item("暂无提交记录")
		return

	var lines: PackedStringArray = result.output.split("\n")
	for line in lines:
		if line.strip_edges().is_empty():
			continue
		log_list.add_item(line.strip_edges())

func _on_log_selected(idx: int) -> void:
	var item_text := log_list.get_item_text(idx)
	var commit_hash := item_text.split(" ")[0]
	if commit_hash.is_empty():
		return

	var result := DFGitManager.run_git(PackedStringArray(["show", "--stat", commit_hash]))
	detail_label.clear()
	if result.exit_code == 0:
		detail_label.add_text(result.output)
	else:
		detail_label.add_text("无法加载提交详情。")
