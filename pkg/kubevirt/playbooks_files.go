package kubevirt

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Ansible playbook file helpers. Playbooks are stored on the app-state
// PVC so their on-disk representation is unchanged from PassGo; only the
// package moves. The inventory that drives ansible-playbook is generated
// from VMIs (see handlers_ansible.go) in M7.

func sanitizePlaybookName(baseDir, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("playbook name is required")
	}
	if strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return "", fmt.Errorf("invalid playbook name")
	}
	matched, err := filepath.Match("*.[yY][aA][mM][lL]", name)
	if err != nil || !matched {
		matched2, _ := filepath.Match("*.[yY][mM][lL]", name)
		if !matched2 {
			return "", fmt.Errorf("playbook name must end in .yml or .yaml")
		}
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.') {
			return "", fmt.Errorf("invalid character in playbook name: %c", r)
		}
	}
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("resolve base dir: %w", err)
	}
	absPath := filepath.Join(absBase, name)
	rel, err := filepath.Rel(absBase, absPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("invalid playbook name")
	}
	return absPath, nil
}

func ListPlaybooks(baseDir string) ([]string, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("read playbooks dir: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		lower := strings.ToLower(e.Name())
		if strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".yaml") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func ReadPlaybook(baseDir, name string) (string, error) {
	path, err := sanitizePlaybookName(baseDir, name)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read playbook: %w", err)
	}
	return string(data), nil
}

func WritePlaybook(baseDir, name, content string) error {
	path, err := sanitizePlaybookName(baseDir, name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return fmt.Errorf("create playbooks dir: %w", err)
	}
	return os.WriteFile(path, []byte(content), 0644)
}

func DeletePlaybook(baseDir, name string) error {
	path, err := sanitizePlaybookName(baseDir, name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("playbook not found")
	}
	return os.Remove(path)
}
