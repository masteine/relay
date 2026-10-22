package setup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/masteine/relay/internal/gh"
)

// RunnerAsset is the macOS tarball name for a release tag and GOARCH.
func RunnerAsset(tag, goarch string) string {
	arch := "arm64"
	if goarch == "amd64" {
		arch = "x64"
	}
	return fmt.Sprintf("actions-runner-osx-%s-%s.tar.gz", arch, strings.TrimPrefix(tag, "v"))
}

// ParseSHA finds the SHA-256 the release notes give for an asset. The notes
// list each asset as
//
//   - actions-runner-osx-arm64-2.338.0.tar.gz <!-- BEGIN SHA osx-arm64 -->df4c…<!-- END SHA osx-arm64 -->
//
// and mention the same name earlier in curl/tar examples, so only the
// marker form counts; without it there is no hash to trust.
func ParseSHA(body, asset string) string {
	re := regexp.MustCompile(regexp.QuoteMeta(asset) + `\s*<!--\s*BEGIN SHA[^>]*-->\s*([0-9a-f]{64})`)
	if m := re.FindStringSubmatch(body); m != nil {
		return m[1]
	}
	return ""
}

// EnsureDist makes sure the runner tarball is in the cache, downloaded from
// github.com/actions/runner and checked against the SHA-256 in the release
// notes. It returns the tarball path and the release tag.
func (s *Sys) EnsureDist(ctx context.Context) (string, string, error) {
	out, err := s.run(ctx, "gh", "api", "repos/actions/runner/releases/latest")
	if err != nil {
		return "", "", err
	}
	var rel struct {
		Tag  string `json:"tag_name"`
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(out), &rel); err != nil {
		return "", "", fmt.Errorf("runner release: %w", err)
	}
	asset := RunnerAsset(rel.Tag, s.Arch)
	want := ParseSHA(rel.Body, asset)
	if want == "" {
		return "", "", fmt.Errorf("runner release %s: no SHA-256 for %s in the notes", rel.Tag, asset)
	}
	if err := os.MkdirAll(s.distDir(), 0o755); err != nil {
		return "", "", err
	}
	path := filepath.Join(s.distDir(), asset)
	if ok, _ := fileSHA(path, want); !ok {
		s.logf("downloading %s (%s)…", asset, rel.Tag)
		_ = os.Remove(path)
		if _, err := s.run(ctx, "gh", "release", "download", rel.Tag, "-R", "actions/runner", "-p", asset, "-D", s.distDir()); err != nil {
			return "", "", err
		}
		if ok, got := fileSHA(path, want); !ok {
			_ = os.Remove(path)
			return "", "", fmt.Errorf("%s: SHA-256 %s does not match the release notes (%s)", asset, got, want)
		}
	}
	return path, rel.Tag, nil
}

func fileSHA(path, want string) (bool, string) {
	f, err := os.Open(path)
	if err != nil {
		return false, ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, ""
	}
	got := hex.EncodeToString(h.Sum(nil))
	return got == want, got
}

// OwnerIsOrg reports whether the repository owner is an organization, in
// which case one org-level runner can serve every repository.
func (s *Sys) OwnerIsOrg(ctx context.Context, owner string) bool {
	out, err := s.run(ctx, "gh", "api", "users/"+owner, "-q", ".type")
	return err == nil && strings.TrimSpace(out) == "Organization"
}

// OrgHasRelayRunner reports whether the organization already has a runner
// with the relay label.
func (s *Sys) OrgHasRelayRunner(ctx context.Context, owner string) bool {
	out, err := s.run(ctx, "gh", "api", "orgs/"+owner+"/actions/runners?per_page=100", "-q", `[.runners[]|select(.labels[].name=="relay")]|length`)
	return err == nil && strings.TrimSpace(out) != "0" && strings.TrimSpace(out) != ""
}

// runnerDirFor is the directory of a repository's runner: owner-name, so
// that a/app and b/app do not collide. A directory from before this naming
// (name only) is reused when its .runner file points at the same repository.
func (s *Sys) runnerDirFor(repo gh.Repo) string {
	preferred := s.runnerDir(repo.Owner + "-" + repo.Name)
	for _, dir := range []string{preferred, s.runnerDir(repo.Name)} {
		if url, ok := configuredURL(dir); ok && strings.EqualFold(url, "https://github.com/"+repo.String()) {
			return dir
		}
	}
	return preferred
}

// configuredURL reads the gitHubUrl of a configured runner directory.
func configuredURL(dir string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(dir, ".runner"))
	if err != nil {
		return "", false
	}
	data = []byte(strings.TrimPrefix(string(data), "\uFEFF"))
	var r struct {
		URL string `json:"gitHubUrl"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return "", false
	}
	return r.URL, r.URL != ""
}

// RegisterRunner installs a self-hosted runner for the repository as a
// LaunchAgent that survives reboots, with the relay label, a clean
// environment (no CLAUDE_*/ANTHROPIC_* from the desktop app) and no
// SessionCreate in its plist. A runner already configured for the same
// repository is kept; one configured for another repository is an error.
func (s *Sys) RegisterRunner(ctx context.Context, repo gh.Repo, tarball string) error {
	dir := s.runnerDirFor(repo)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, "config.sh")); err != nil {
		s.logf("extracting the runner into %s…", dir)
		if _, err := s.run(ctx, "tar", "-xzf", tarball, "-C", dir); err != nil {
			return err
		}
	}
	switch url, ok := configuredURL(dir); {
	case ok && strings.EqualFold(url, "https://github.com/"+repo.String()):
		s.logf("runner in %s is already configured for %s", dir, repo)
	case ok:
		return fmt.Errorf("%s holds a runner for %s, not %s; remove it first (`./config.sh remove`)", dir, url, repo)
	default:
		tok, err := s.run(ctx, "gh", "api", "-X", "POST", fmt.Sprintf("repos/%s/actions/runners/registration-token", repo), "-q", ".token")
		if err != nil {
			return fmt.Errorf("registration token for %s: %w", repo, err)
		}
		host, _ := os.Hostname()
		host = strings.TrimSuffix(strings.ToLower(host), ".local")
		name := fmt.Sprintf("relay-%s-%s", host, repo.Name)
		if len(name) > 64 {
			name = name[:64]
		}
		bin, args := s.cleanEnvArgs(dir, "./config.sh", "--unattended", "--url", "https://github.com/"+repo.String(),
			"--token", strings.TrimSpace(tok), "--labels", "relay", "--name", name, "--replace", "--work", "_work")
		if _, err := s.run(ctx, bin, args...); err != nil {
			return fmt.Errorf("configure runner: %w", err)
		}
		s.logf("runner %s registered for %s", name, repo)
	}
	if _, err := os.Stat(filepath.Join(dir, ".service")); err != nil {
		bin, args := s.cleanEnvArgs(dir, "./svc.sh", "install")
		if _, err := s.run(ctx, bin, args...); err != nil {
			return fmt.Errorf("install runner service: %w", err)
		}
	}
	plist := s.runnerPlist(filepath.Base(dir))
	if plist == "" {
		return fmt.Errorf("runner service file .service not found in %s", dir)
	}
	changed, err := StripSessionCreate(plist)
	if err != nil {
		return err
	}
	if changed {
		s.logf("removed SessionCreate from %s (the service must see the login Keychain)", filepath.Base(plist))
	}
	bin, args := s.cleanEnvArgs(dir, "./svc.sh", "stop")
	_, _, _, _ = s.Exec(ctx, bin, args...)
	bin, args = s.cleanEnvArgs(dir, "./svc.sh", "start")
	if _, err := s.run(ctx, bin, args...); err != nil {
		return fmt.Errorf("start runner service: %w", err)
	}
	s.logf("runner service for %s started", repo)
	return nil
}
