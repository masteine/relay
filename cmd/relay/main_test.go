package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunVersionAndUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "protocol v1") {
		t.Errorf("version: code=%d out=%q", code, out.String())
	}
	out.Reset()
	if code := run(nil, &out, &errb); code != 2 || !strings.Contains(errb.String(), "Usage:") {
		t.Errorf("no args: code=%d err=%q", code, errb.String())
	}
	errb.Reset()
	if code := run([]string{"bogus"}, &out, &errb); code != 2 || !strings.Contains(errb.String(), `unknown command "bogus"`) {
		t.Errorf("unknown: code=%d err=%q", code, errb.String())
	}
	errb.Reset()
	if code := run([]string{"stage", "triage", "-h"}, &out, &errb); code != 0 {
		t.Errorf("-h: code=%d err=%q", code, errb.String())
	}
}

func TestStageRefusesWithoutProtocolAndRepo(t *testing.T) {
	var out, errb bytes.Buffer
	t.Setenv("RELAY_PROTOCOL", "")
	t.Setenv("GITHUB_REPOSITORY", "")
	if code := run([]string{"stage", "triage", "--issue", "1"}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "protocol") {
		t.Errorf("no protocol: code=%d err=%q", code, errb.String())
	}
	errb.Reset()
	t.Setenv("RELAY_PROTOCOL", "1")
	if code := run([]string{"stage", "triage", "--issue", "1"}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "GITHUB_REPOSITORY") {
		t.Errorf("no repo: code=%d err=%q", code, errb.String())
	}
	errb.Reset()
	t.Setenv("RELAY_PROTOCOL", "7")
	if code := run([]string{"stage", "route"}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "v7") {
		t.Errorf("mismatch: code=%d err=%q", code, errb.String())
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"add", "onlyone"}, &out, &errb); code != 2 {
		t.Errorf("add with one arg: code=%d err=%q", code, errb.String())
	}
	if code := run([]string{"add", "x", "not-a-repo"}, &out, &errb); code != 2 {
		t.Errorf("add with a bad repo: code=%d", code)
	}
	if code := run([]string{"doctor", "--check-issue", "3"}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "--repo") {
		t.Errorf("doctor without --repo: code=%d err=%q", code, errb.String())
	}
	if code := run([]string{"setup", "--bogus"}, &out, &errb); code != 2 {
		t.Errorf("unknown flag: code=%d", code)
	}
}
