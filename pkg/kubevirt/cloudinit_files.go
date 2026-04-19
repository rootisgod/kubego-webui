package kubevirt

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// File-based cloud-init template helpers. These are pure filesystem
// operations — not KubeVirt-coupled — and survive the rewrite unchanged
// so handlers that read and write template files keep working.

func scanCloudInitTemplates(searchDirs []string) ([]TemplateOption, error) {
	seen := make(map[string]struct{})
	var options []TemplateOption

	for _, dir := range searchDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			lower := strings.ToLower(name)
			if !strings.HasSuffix(lower, ".yml") && !strings.HasSuffix(lower, ".yaml") {
				continue
			}
			path := filepath.Join(dir, name)
			absPath, err := filepath.Abs(path)
			if err != nil {
				continue
			}
			if _, ok := seen[absPath]; ok {
				continue
			}
			if !hasCloudConfigHeader(absPath) {
				continue
			}
			seen[absPath] = struct{}{}
			options = append(options, TemplateOption{Label: name, Path: absPath})
		}
	}
	return options, nil
}

func hasCloudConfigHeader(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	if scanner.Scan() {
		return strings.TrimSpace(scanner.Text()) == "#cloud-config"
	}
	return false
}

// getAllCloudInitTemplates is the package-local helper; the Client
// interface exposes it as GetAllCloudInitTemplates so handlers can
// dispatch through the driver.
func getAllCloudInitTemplates(configuredDirs []string) ([]TemplateOption, error) {
	var dirs []string
	dirs = append(dirs, configuredDirs...)
	if exePath, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exePath))
	}
	if cwd, err := os.Getwd(); err == nil {
		dirs = append(dirs, cwd)
	}

	seen := make(map[string]struct{})
	var unique []string
	for _, d := range dirs {
		if d == "" {
			continue
		}
		abs, err := filepath.Abs(d)
		if err != nil {
			continue
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		unique = append(unique, abs)
	}
	return scanCloudInitTemplates(unique)
}

// CleanupTempDirs removes temporary directories. Kept as a package
// function so handlers can dispose of scratch dirs at shutdown.
func CleanupTempDirs(dirs []string) {
	for _, d := range dirs {
		if d != "" {
			os.RemoveAll(d)
		}
	}
}

// InjectSSHAuthorizedKey merges `pubKey` into the cloud-init document's
// top-level `ssh_authorized_keys` list. Cloud-init applies that list to
// the default cloud user (on Ubuntu images: `ubuntu`), so this is the
// least-invasive way to guarantee we can SSH in without interfering
// with any user-supplied `users:` block.
//
// If content is empty, a minimal `#cloud-config` document is created.
// Idempotent: re-injecting the same key is a no-op. Existing entries
// are preserved.
func InjectSSHAuthorizedKey(content, pubKey string) (string, error) {
	pubKey = strings.TrimSpace(pubKey)
	if pubKey == "" {
		return content, nil
	}

	if strings.TrimSpace(content) == "" {
		content = "#cloud-config\n"
	}

	var doc map[string]any
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return "", fmt.Errorf("parse cloud-init yaml: %w", err)
	}
	if doc == nil {
		doc = map[string]any{}
	}

	// Existing keys may come back as []any or []string depending on
	// whether YAML parsed them into typed slices. Normalise to []any.
	var keys []any
	switch v := doc["ssh_authorized_keys"].(type) {
	case nil:
	case []any:
		keys = v
	case []string:
		for _, s := range v {
			keys = append(keys, s)
		}
	default:
		return "", fmt.Errorf("unexpected type for ssh_authorized_keys: %T", v)
	}
	for _, k := range keys {
		if s, ok := k.(string); ok && strings.TrimSpace(s) == pubKey {
			return content, nil // already present
		}
	}
	keys = append(keys, pubKey)
	doc["ssh_authorized_keys"] = keys

	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("marshal cloud-init yaml: %w", err)
	}
	return "#cloud-config\n" + string(out), nil
}

// InjectQEMUGuestAgent appends a distro-appropriate install command for
// qemu-guest-agent to the cloud-init `runcmd` list. `distro` is the
// package-manager family returned by ImageDistro — "debian" for apt-based
// images, "rhel" for dnf-based ones. An unknown or empty distro is a
// no-op: we'd rather not run than break a guest we can't reason about.
//
// Idempotent: if the existing runcmd already mentions qemu-guest-agent, the
// content is returned unchanged so re-launches and re-applied profiles
// don't stack duplicates.
func InjectQEMUGuestAgent(content, distro string) (string, error) {
	cmd := guestAgentCommand(distro)
	if cmd == "" {
		return content, nil
	}

	if strings.TrimSpace(content) == "" {
		content = "#cloud-config\n"
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return "", fmt.Errorf("parse cloud-init yaml: %w", err)
	}
	if doc == nil {
		doc = map[string]any{}
	}

	var runcmd []any
	switch v := doc["runcmd"].(type) {
	case nil:
	case []any:
		runcmd = v
	default:
		return "", fmt.Errorf("unexpected type for runcmd: %T", v)
	}
	for _, entry := range runcmd {
		if containsQEMUGuestAgent(entry) {
			return content, nil
		}
	}
	// Use exec form [sh, -c, "…"] so cloud-init runs the full pipeline
	// in a shell without per-token splitting.
	runcmd = append(runcmd, []any{"sh", "-c", cmd})
	doc["runcmd"] = runcmd

	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("marshal cloud-init yaml: %w", err)
	}
	return "#cloud-config\n" + string(out), nil
}

func guestAgentCommand(distro string) string {
	switch distro {
	case "debian":
		return "DEBIAN_FRONTEND=noninteractive apt-get update -y && DEBIAN_FRONTEND=noninteractive apt-get install -y qemu-guest-agent && systemctl enable --now qemu-guest-agent"
	case "rhel":
		return "dnf install -y qemu-guest-agent && systemctl enable --now qemu-guest-agent"
	}
	return ""
}

func containsQEMUGuestAgent(entry any) bool {
	switch v := entry.(type) {
	case string:
		return strings.Contains(v, "qemu-guest-agent")
	case []any:
		for _, item := range v {
			if containsQEMUGuestAgent(item) {
				return true
			}
		}
	}
	return false
}

// InjectHostname sets the top-level `hostname` and `fqdn` keys on a
// cloud-init document so the VM comes up with a name that matches what
// the UI asked for, rather than the generic "ubuntu"/"fedora" default.
// Empty hostname is a no-op.
func InjectHostname(content, hostname string) (string, error) {
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return content, nil
	}
	if strings.TrimSpace(content) == "" {
		content = "#cloud-config\n"
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return "", fmt.Errorf("parse cloud-init yaml: %w", err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	doc["hostname"] = hostname
	doc["fqdn"] = hostname
	doc["preserve_hostname"] = false
	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("marshal cloud-init yaml: %w", err)
	}
	return "#cloud-config\n" + string(out), nil
}

// InjectUserWithPassword adds a sudo-capable `username` user to the
// cloud-init document and sets its password via chpasswd with
// `expire: false`. When sshPubKey is non-empty it also seeds the user's
// authorized_keys so the caller can SSH in as that account immediately.
//
// Idempotent-ish: if a user with the same name already exists in the
// users block we leave it alone (the operator presumably meant it) and
// still append to chpasswd so the password lands. Empty username or
// password is a no-op.
func InjectUserWithPassword(content, username, password, sshPubKey string) (string, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return content, nil
	}
	if strings.TrimSpace(content) == "" {
		content = "#cloud-config\n"
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return "", fmt.Errorf("parse cloud-init yaml: %w", err)
	}
	if doc == nil {
		doc = map[string]any{}
	}

	// Users list — normalise, then append if the user isn't present.
	var users []any
	switch v := doc["users"].(type) {
	case nil:
		// Preserve the distro default user ("ubuntu", "fedora", …) by
		// keeping the cloud-init shortcut `default` at position 0.
		users = []any{"default"}
	case []any:
		users = v
	default:
		return "", fmt.Errorf("unexpected type for users: %T", v)
	}
	alreadyHasUser := false
	for _, u := range users {
		if m, ok := u.(map[string]any); ok {
			if n, _ := m["name"].(string); n == username {
				alreadyHasUser = true
				break
			}
		}
	}
	if !alreadyHasUser {
		entry := map[string]any{
			"name":        username,
			"groups":      "sudo",
			"sudo":        "ALL=(ALL) NOPASSWD:ALL",
			"shell":       "/bin/bash",
			"lock_passwd": false,
		}
		if key := strings.TrimSpace(sshPubKey); key != "" {
			entry["ssh_authorized_keys"] = []any{key}
		}
		users = append(users, entry)
	}
	doc["users"] = users

	// chpasswd sets the password. `expire: false` skips the force-change-
	// on-first-login prompt, which is what a lab tool wants. Plain-text
	// format — cloud-init hashes and applies it inside the guest.
	chpasswd, _ := doc["chpasswd"].(map[string]any)
	if chpasswd == nil {
		chpasswd = map[string]any{}
	}
	existing, _ := chpasswd["list"].(string)
	line := username + ":" + password
	if !strings.Contains(existing, line) {
		if existing == "" {
			chpasswd["list"] = line + "\n"
		} else {
			chpasswd["list"] = strings.TrimRight(existing, "\n") + "\n" + line + "\n"
		}
	}
	if _, ok := chpasswd["expire"]; !ok {
		chpasswd["expire"] = false
	}
	doc["chpasswd"] = chpasswd

	// SSH password auth so the user can log in over SSH with the
	// password (cloud-init default varies by distro).
	doc["ssh_pwauth"] = true

	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("marshal cloud-init yaml: %w", err)
	}
	return "#cloud-config\n" + string(out), nil
}

// ValidateCloudInitYAML checks that content is valid cloud-init YAML.
// The first non-empty line must be "#cloud-config" — the same rule
// cloud-init itself enforces in the guest.
func ValidateCloudInitYAML(content string) error {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return fmt.Errorf("content is empty")
	}
	lines := strings.SplitN(trimmed, "\n", 2)
	if strings.TrimSpace(lines[0]) != "#cloud-config" {
		return fmt.Errorf("first line must be '#cloud-config'")
	}
	var doc any
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return fmt.Errorf("invalid YAML: %w", err)
	}
	return nil
}

func sanitizeTemplateName(baseDir, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("template name is required")
	}
	if strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return "", fmt.Errorf("invalid template name")
	}
	matched, err := filepath.Match("*.[yY][aA][mM][lL]", name)
	if err != nil || !matched {
		matched2, _ := filepath.Match("*.[yY][mM][lL]", name)
		if !matched2 {
			return "", fmt.Errorf("template name must end in .yml or .yaml")
		}
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.') {
			return "", fmt.Errorf("invalid character in template name: %c", r)
		}
	}
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("resolve base dir: %w", err)
	}
	absPath := filepath.Join(absBase, name)
	rel, err := filepath.Rel(absBase, absPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("invalid template name")
	}
	return absPath, nil
}

func ReadCloudInitTemplate(baseDir, name string) (string, error) {
	path, err := sanitizeTemplateName(baseDir, name)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read template: %w", err)
	}
	return string(data), nil
}

func WriteCloudInitTemplate(baseDir, name, content string) error {
	path, err := sanitizeTemplateName(baseDir, name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return fmt.Errorf("create cloud-init dir: %w", err)
	}
	return os.WriteFile(path, []byte(content), 0644)
}

func DeleteCloudInitTemplate(baseDir, name string) error {
	path, err := sanitizeTemplateName(baseDir, name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("template not found")
	}
	return os.Remove(path)
}
