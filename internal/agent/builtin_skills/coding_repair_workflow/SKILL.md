AFFENT ACTIVE SKILL: coding_repair_workflow

Use this procedure for code changes:
- Reproduce first with the narrowest relevant test or command before editing, unless the user only asked for analysis.
- Run test/build commands directly: use commands like go test ./..., python -m pytest, npm test, etc. Do not wrap the first reproduction in cd ... && ... | head, ; echo "$?", or || true.
- Inspect the failing code and the failing test/spec. Change implementation files by default; do not edit tests unless the user asks or the test is clearly wrong.
- Keep the patch small and coherent. Prefer surgical edit_file changes over broad rewrites. Every edit_file call must include a workspace-relative path plus an exact old string from the current file contents; call read_file again when the current text is uncertain.
- Preserve verification exit codes. Do not pipe tests/builds through head/tail, append "|| true", or append "echo $?" wrappers; rely on the shell tool's exit code line, or redirect output to a file and inspect chunks after the command finishes.
- Do not add or install a new dependency to fix a failing test unless the project manifest already declares that dependency or the user explicitly asks for dependency work. Prefer standard-library fixes when they are enough.
- Do not import a third-party package just because it is installed in the current environment. If the manifest does not declare it, treat it as unavailable; prefer standard-library implementations.
- If a build/test tool is not on PATH, do bounded discovery: command -v, repo-local toolchains such as ./.tmp/toolchains, and common user-local paths such as $HOME/.local. Do not run broad filesystem searches like find /.
- After editing, run the same failing command again. If the language has a standard formatter and it is available, run it before the final test.
- If the task asks you to commit, push, or leave a clean working tree in a git repository, run `git status --short` as its own shell command after the final push and before the final answer.
- In the final answer, state the files changed and the exact verification command/result.
