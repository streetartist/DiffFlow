@tool
extends VBoxContainer

## 主面板 - 包含所有 DiffFlow 子面板。

var sync_engine: DFSyncEngine

@onready var tab_container: TabContainer = $TabContainer

func _ready() -> void:
	_setup_panels()

func _setup_panels() -> void:
	for child in tab_container.get_children():
		child.queue_free()

	var collab_panel := preload("res://addons/diffflow/ui/dock/collab_panel.tscn").instantiate()
	collab_panel.name = "协作"
	tab_container.add_child(collab_panel)
	if sync_engine:
		collab_panel.initialize(sync_engine)

	var git_panel := preload("res://addons/diffflow/ui/dock/git_panel.tscn").instantiate()
	git_panel.name = "版本"
	tab_container.add_child(git_panel)

	var diff_viewer := preload("res://addons/diffflow/ui/dock/diff_viewer.tscn").instantiate()
	diff_viewer.name = "差异"
	tab_container.add_child(diff_viewer)

	var log_viewer := preload("res://addons/diffflow/ui/dock/log_viewer.tscn").instantiate()
	log_viewer.name = "日志"
	tab_container.add_child(log_viewer)
