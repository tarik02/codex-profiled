package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMaterializeShadowHomeSharesEntries(t *testing.T) {
	root := t.TempDir()
	sharedHome := filepath.Join(root, ".codex")
	shadowHome := filepath.Join(root, ".codex-work")

	if err := os.MkdirAll(sharedHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sharedHome, "config.toml"), []byte("model = \"test\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sharedHome, "history.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	layout := shadowHomeLayout{
		sharedHomePath:    sharedHome,
		effectiveHomePath: shadowHome,
	}
	if err := materializeShadowHome(layout); err != nil {
		t.Fatal(err)
	}

	if got, err := os.ReadFile(filepath.Join(shadowHome, "config.toml")); err != nil {
		t.Fatal(err)
	} else if string(got) != "model = \"test\"\n" {
		t.Fatalf("shadow config = %q, want shared config contents", got)
	}

	if got, err := os.ReadFile(filepath.Join(shadowHome, "history.jsonl")); err != nil {
		t.Fatal(err)
	} else if string(got) != "{}\n" {
		t.Fatalf("shadow history = %q, want shared history contents", got)
	}

	sessionFile := filepath.Join(sharedHome, "sessions", "session.jsonl")
	if err := os.WriteFile(sessionFile, []byte("session\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(shadowHome, "sessions", "session.jsonl")); err != nil {
		t.Fatal(err)
	} else if string(got) != "session\n" {
		t.Fatalf("shadow session = %q, want shared session contents", got)
	}

	authPath := filepath.Join(shadowHome, "auth.json")
	if state, _, err := readLinkState(authPath); err != nil {
		t.Fatal(err)
	} else if state == linkSymlink {
		t.Fatalf("shadow auth must be private, got symlink at %s", authPath)
	}
}

func TestReadLinkStateTreatsRegularFileAsNotSymlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	state, target, err := readLinkState(path)
	if err != nil {
		t.Fatal(err)
	}
	if state != linkNotSymlink {
		t.Fatalf("state = %v, want %v", state, linkNotSymlink)
	}
	if target != "" {
		t.Fatalf("target = %q, want empty", target)
	}
}
