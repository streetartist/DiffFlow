@tool
extends ConfirmationDialog
class_name DFConnectDialog

## Dialog for collecting DiffFlow account credentials.

signal login_requested(url: String, username: String, password: String)

var _url_edit: LineEdit
var _username_edit: LineEdit
var _password_edit: LineEdit

func _ready() -> void:
	title = "Connect to DiffFlow Server"
	min_size = Vector2i(400, 200)

	var vbox := VBoxContainer.new()
	add_child(vbox)

	vbox.add_child(_make_label("Server URL"))
	_url_edit = LineEdit.new()
	_url_edit.text = DFSettings.get_server_url()
	_url_edit.placeholder_text = "http://localhost:8090"
	vbox.add_child(_url_edit)

	vbox.add_child(_make_label("Username"))
	_username_edit = LineEdit.new()
	_username_edit.text = DFSettings.get_username()
	_username_edit.placeholder_text = "Username"
	vbox.add_child(_username_edit)

	vbox.add_child(_make_label("Password"))
	_password_edit = LineEdit.new()
	_password_edit.placeholder_text = "Password"
	_password_edit.secret = true
	vbox.add_child(_password_edit)

	confirmed.connect(_on_confirmed)

func _make_label(text: String) -> Label:
	var l := Label.new()
	l.text = text
	return l

func _on_confirmed() -> void:
	var url := _url_edit.text.strip_edges()
	var username := _username_edit.text.strip_edges()
	var password := _password_edit.text

	DFSettings.set_server_url(url)
	DFSettings.set_username(username)

	login_requested.emit(url, username, password)
