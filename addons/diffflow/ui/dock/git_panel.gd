@tool
extends VBoxContainer

## Git operations panel with status display, staging, and common git commands.

signal commit_requested(message: String)
signal push_requested
signal pull_requested

@onready var status_list: ItemList = $StatusList
@onready var commit_msg: TextEdit = $CommitMsg
@onready var branch_label: Label = $BranchBar/BranchLabel
@onready var branch_option: OptionButton = $BranchBar/BranchOption

func _ready() -> void:
	$ActionBar/RefreshBtn.pressed.connect(_on_refresh)
	$ActionBar/CommitBtn.pressed.connect(_on_commit)
	$ActionBar/PushBtn.pressed.connect(_on_push)
	$ActionBar/PullBtn.pressed.connect(_on_pull)
	$BranchBar/NewBranchBtn.pressed.connect(_on_new_branch)
	$BranchBar/BranchOption.item_selected.connect(_on_branch_selected)

func _refresh_status() -> void:
	var result := DFGitManager.status()
	status_list.clear()
	if result.exit_code != 0:
		status_list.add_item("非 Git 仓库")
		return

	var lines: PackedStringArray = result.output.split("\n")
	for line in lines:
		if line.strip_edges().is_empty():
			continue
		var status_code := line.substr(0, 2)
		var fpath := line.substr(3).strip_edges()
		var icon_text := _status_icon(status_code)
		status_list.add_item(icon_text + " " + fpath)

	if status_list.item_count == 0:
		status_list.add_item("工作区干净")

func _refresh_branches() -> void:
	branch_option.clear()
	var result := DFGitManager.branch_list()
	if result.exit_code != 0:
		return
	var current_branch := ""
	var lines: PackedStringArray = result.output.split("\n")
	for line in lines:
		var bname := line.strip_edges()
		if bname.is_empty():
			continue
		if bname.begins_with("* "):
			bname = bname.substr(2)
			current_branch = bname
		branch_option.add_item(bname)

	for i in range(branch_option.item_count):
		if branch_option.get_item_text(i) == current_branch:
			branch_option.selected = i
			break
	branch_label.text = "分支: " + current_branch

func _on_refresh() -> void:
	_refresh_status()
	_refresh_branches()

func _on_commit() -> void:
	var msg := commit_msg.text.strip_edges()
	if msg.is_empty():
		_show_message("请输入提交说明。")
		return
	DFGitManager.add(PackedStringArray(["."]))
	var result := DFGitManager.commit(msg)
	if result.exit_code == 0:
		commit_msg.text = ""
		_show_message("提交成功。")
	else:
		_show_message("提交失败: " + result.output)
	_refresh_status()

func _on_push() -> void:
	var result := DFGitManager.push()
	if result.exit_code == 0:
		_show_message("推送成功。")
	else:
		_show_message("推送失败: " + result.output)

func _on_pull() -> void:
	var result := DFGitManager.pull()
	if result.exit_code == 0:
		_show_message("拉取成功。")
	else:
		_show_message("拉取失败: " + result.output)
	_refresh_status()

func _on_new_branch() -> void:
	var dialog := AcceptDialog.new()
	dialog.title = "新建分支"
	var line_edit := LineEdit.new()
	line_edit.placeholder_text = "分支名称"
	dialog.add_child(line_edit)
	dialog.confirmed.connect(func() -> void:
		var bname := line_edit.text.strip_edges()
		if not bname.is_empty():
			DFGitManager.branch_create(bname)
			DFGitManager.checkout(bname)
			_refresh_branches()
		dialog.queue_free()
	)
	dialog.canceled.connect(dialog.queue_free)
	add_child(dialog)
	dialog.popup_centered(Vector2i(300, 80))

func _on_branch_selected(idx: int) -> void:
	var bname := branch_option.get_item_text(idx)
	DFGitManager.checkout(bname)
	_refresh_branches()
	_refresh_status()

func _status_icon(code: String) -> String:
	var c := code.strip_edges()
	if c == "M" or c == " M":
		return "[M]"
	elif c == "A" or c == " A":
		return "[A]"
	elif c == "D" or c == " D":
		return "[D]"
	elif c == "R":
		return "[R]"
	elif c == "??":
		return "[?]"
	elif c == "UU":
		return "[C]"
	else:
		return "[" + c + "]"

func _show_message(msg: String) -> void:
	var dialog := AcceptDialog.new()
	dialog.dialog_text = msg
	dialog.confirmed.connect(dialog.queue_free)
	add_child(dialog)
	dialog.popup_centered()
