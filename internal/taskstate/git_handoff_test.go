package taskstate

import "testing"

func TestShellHandoffEvidenceSourceClassifiesGitCommitAndPush(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
		want    string
	}{
		{name: "commit", command: "git commit -m fix", want: GitCommitEvidenceSource},
		{name: "commit with worktree", command: "git -C app commit -m fix", want: GitCommitEvidenceSource},
		{name: "push", command: "git push origin main", want: GitPushEvidenceSource},
		{name: "push with config", command: "git -c advice.detachedHead=false push origin main", want: GitPushEvidenceSource},
		{name: "status", command: "git status --short", want: ""},
		{name: "non git", command: "go test ./...", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShellHandoffEvidenceSource(tc.command); got != tc.want {
				t.Fatalf("ShellHandoffEvidenceSource(%q) = %q, want %q", tc.command, got, tc.want)
			}
		})
	}
}

func TestGitSubcommandSkipsGlobalOptions(t *testing.T) {
	for _, tc := range []struct {
		command string
		want    string
	}{
		{command: "git --git-dir=.git --work-tree=. commit -m fix", want: "commit"},
		{command: "git --namespace=eval push origin main", want: "push"},
		{command: "git -Capp status --short", want: "status"},
		{command: "git -cuser.name=Affent commit -m fix", want: "commit"},
	} {
		if got := GitSubcommand(tc.command); got != tc.want {
			t.Fatalf("GitSubcommand(%q) = %q, want %q", tc.command, got, tc.want)
		}
	}
}

func TestShellHandoffEvidenceSourcesClassifiesChainedGitCommands(t *testing.T) {
	got := ShellHandoffEvidenceSources(`git add greet/greet.go && git commit -m "fix guest" && git push origin main`)
	want := []string{GitCommitEvidenceSource, GitPushEvidenceSource}
	if len(got) != len(want) {
		t.Fatalf("sources = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sources = %#v, want %#v", got, want)
		}
	}
}
