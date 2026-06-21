package main

import "testing"

func TestValidateProfileNameRejectsPortableInvalidNames(t *testing.T) {
	invalid := []string{
		"",
		" ",
		".",
		"..",
		"work/dev",
		`work\dev`,
		"work:dev",
		`work"dev`,
		"work<dev",
		"work>dev",
		"work|dev",
		"work?dev",
		"work*dev",
		"work\x00dev",
	}

	for _, profile := range invalid {
		if err := validateProfileName(profile); err == nil {
			t.Fatalf("validateProfileName(%q) succeeded, want error", profile)
		}
	}
}

func TestValidateProfileNameAcceptsSimpleNames(t *testing.T) {
	valid := []string{"work", "work-dev", "work_dev", "work.dev", "work 2"}

	for _, profile := range valid {
		if err := validateProfileName(profile); err != nil {
			t.Fatalf("validateProfileName(%q) failed: %v", profile, err)
		}
	}
}
