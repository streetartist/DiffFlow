@tool
extends RefCounted
class_name DFGitManager

## Wrapper around git CLI for version control operations.

static func run_git(args: PackedStringArray) -> Dictionary:
	var output: Array = []
	var exit_code := OS.execute("git", args, output, true)
	return {
		"exit_code": exit_code,
		"output": "\n".join(output).strip_edges()
	}

static func status() -> Dictionary:
	return run_git(PackedStringArray(["status", "--porcelain"]))

static func add(paths: PackedStringArray = PackedStringArray(["."])) -> Dictionary:
	var args := PackedStringArray(["add"])
	args.append_array(paths)
	return run_git(args)

static func commit(message: String) -> Dictionary:
	return run_git(PackedStringArray(["commit", "-m", message]))

static func push(remote: String = "origin", branch: String = "") -> Dictionary:
	var args := PackedStringArray(["push", remote])
	if branch != "":
		args.append(branch)
	return run_git(args)

static func pull(remote: String = "origin", branch: String = "") -> Dictionary:
	var args := PackedStringArray(["pull", remote])
	if branch != "":
		args.append(branch)
	return run_git(args)

static func branch_list() -> Dictionary:
	return run_git(PackedStringArray(["branch", "--list"]))

static func branch_create(branch_name: String) -> Dictionary:
	return run_git(PackedStringArray(["branch", branch_name]))

static func checkout(branch: String) -> Dictionary:
	return run_git(PackedStringArray(["checkout", branch]))

static func diff(path: String = "") -> Dictionary:
	var args := PackedStringArray(["diff"])
	if path != "":
		args.append(path)
	return run_git(args)

static func log_short(count: int = 20) -> Dictionary:
	return run_git(PackedStringArray(["log", "--oneline", "-n", str(count)]))

static func init() -> Dictionary:
	return run_git(PackedStringArray(["init"]))
