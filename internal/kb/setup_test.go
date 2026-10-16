package kb

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPermPath(t *testing.T) {
	cases := map[string]string{
		`C:\work\my-kb`: "//c/work/my-kb",
		`D:\x\`:         "//d/x",
		"/home/you/kb":  "//home/you/kb",
	}
	if runtime.GOOS != "windows" {
		delete(cases, `C:\work\my-kb`)
		delete(cases, `D:\x\`)
	}
	for in, want := range cases {
		if got := permPath(in); got != want {
			t.Errorf("permPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSettingLines(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	lines, bad := settingLines(get)
	if len(bad) != 0 || !strings.Contains(lines[0], "when statements worth recording") || !strings.Contains(lines[1], "one at a time") {
		t.Errorf("defaults: %q %q", lines, bad)
	}
	env[askModeEnv], env[perTurnEnv] = "up-front", "2"
	lines, bad = settingLines(get)
	if len(bad) != 0 || !strings.Contains(lines[0], "up front") || !strings.Contains(lines[1], "up to 2") {
		t.Errorf("set: %q %q", lines, bad)
	}
	env[askModeEnv], env[perTurnEnv] = "always", "0"
	if _, bad = settingLines(get); len(bad) != 2 {
		t.Errorf("bad values not reported: %q", bad)
	}
}

// TestKentSettingsRoundTrip checks that the settings kb setup prints are valid JSON and, written to a
// project, leave nothing missing.
func TestKentSettingsRoundTrip(t *testing.T) {
	project, kb := t.TempDir(), t.TempDir()
	text := kentSettingsJSON(kb, [][2]string{{askModeEnv, "up-front"}})
	var v map[string]any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, text)
	}
	os.MkdirAll(filepath.Join(project, ".claude"), 0o755)
	if err := os.WriteFile(filepath.Join(project, filepath.FromSlash(settingsRel)), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	sf := readSettings(project)
	if sf.err != nil || !sf.hasHook {
		t.Fatalf("read back: err %v, hook %v", sf.err, sf.hasHook)
	}
	if m := missingFrom(sf.allow, kentAllow(kb)); len(m) != 0 {
		t.Errorf("allow missing %q", m)
	}
	if m := missingFrom(sf.deny, kentDeny(kb)); len(m) != 0 {
		t.Errorf("deny missing %q", m)
	}
	if !samePath(absPath(sf.env[kbEnv]), kb) || sf.env[askModeEnv] != "up-front" {
		t.Errorf("env %q", sf.env)
	}
}

// TestSessionStartFits checks that what the hook prints stays under Claude Code's limit, with room for
// the facts kb adds about the project.
func TestSessionStartFits(t *testing.T) {
	doc := readDoc(sessionStartDoc)
	if len(doc) > sessionStartLimit-2000 {
		t.Errorf("%s is %d characters; the hook's output is cut off beyond %d", sessionStartDoc, len(doc), sessionStartLimit)
	}
	if readDoc(setupDoc) == "" {
		t.Errorf("%s is not built in", setupDoc)
	}
}

func TestFTSQuery(t *testing.T) {
	cases := map[string]string{
		"session-start hook":  `"session-start" hook`,
		"kb OR setup":         "kb OR setup",
		`"exact phrase" x`:    `"exact phrase" x`,
		"onboard*":            "onboard*",
		"settings.local.json": `"settings.local.json"`,
	}
	for in, want := range cases {
		if got := ftsQuery([]string{in}); got != want {
			t.Errorf("ftsQuery(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestUserByFolder checks ~/.kenttheclerk.json's per-folder ids: the most specific folder holding the
// knowledge base wins, "user" applies elsewhere, and with no knowledge base chosen only "user" counts.
func TestUserByFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("KB_USER", "")
	work := filepath.Join(home, "work")
	client := filepath.Join(work, "client")
	cfg, _ := json.Marshal(map[string]any{
		"user":    "jamie",
		"folders": map[string]string{work: "jamie-work", client: "jamie@client.example"},
	})
	if err := os.WriteFile(filepath.Join(home, ".kenttheclerk.json"), cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		filepath.Join(client, "kb"):         "jamie@client.example",
		filepath.Join(work, "other-kb"):     "jamie-work",
		filepath.Join(home, "work-kb"):      "jamie",
		filepath.Join(home, "elsewhere-kb"): "jamie",
		"":                                  "jamie",
	}
	for root, want := range cases {
		if got, _ := userSource("", root); got != want {
			t.Errorf("userSource(%q) = %q, want %q", root, got, want)
		}
	}
	if got, _ := userSource("explicit", filepath.Join(client, "kb")); got != "explicit" {
		t.Errorf("--user did not win: %q", got)
	}
}

// TestSetupWantsUnionMerge checks that kb setup asks for merge=union on the logs and the user table of a
// knowledge base in git (S-8MW2), and is satisfied once .gitattributes holds it.
func TestSetupWantsUnionMerge(t *testing.T) {
	root := newTestKB(t)
	if _, st := git(root, "init", "-q"); st != 0 {
		t.Skip("git is not available")
	}
	var r report
	kbReport(&r, root)
	if todo := strings.Join(r.todos(), "\n"); !strings.Contains(todo, "merge=union") || !strings.Contains(todo, "users.jsonl") {
		t.Fatalf("setup did not ask for merge=union:\n%s", todo)
	}
	attrs := "overrides.jsonl merge=union\nusers-log.jsonl merge=union\nusers.jsonl merge=union\n"
	if err := os.WriteFile(filepath.Join(root, ".gitattributes"), []byte(attrs), 0o666); err != nil {
		t.Fatal(err)
	}
	r = report{}
	kbReport(&r, root)
	if todo := strings.Join(r.todos(), "\n"); strings.Contains(todo, "merge=union") {
		t.Fatalf("setup still asks for merge=union:\n%s", todo)
	}
}
