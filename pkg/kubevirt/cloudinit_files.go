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
