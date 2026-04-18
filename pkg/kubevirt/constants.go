package kubevirt

import (
	"fmt"
	"regexp"
)

const (
	DefaultUbuntuRelease = "24.04"
	DefaultCPUCores      = 2
	DefaultRAMMB         = 2048
	DefaultDiskGB        = 16
	MinCPUCores          = 1
	MinRAMMB             = 512
	MinResizeRAMMB       = 256
	MinDiskGB            = 1
	VMNamePrefix         = "vm-"
	VMNameRandomLength   = 4
)

var UbuntuReleases = []string{"24.04", "22.04", "20.04", "daily"}

// Name patterns are tighter than multipass: KubeVirt names must be
// RFC 1123 DNS labels (lowercase alphanumeric + hyphen, start/end with
// alphanumeric). We mirror that here for VM names; group/profile/playbook
// names keep the more permissive app-level rules because they are
// filesystem keys, not Kubernetes resource names.
var (
	vmNamePattern       = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	groupNamePattern    = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9 _-]{0,62}$`)
	profileIDPattern    = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,62}$`)
	playbookFilePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,62}\.ya?ml$`)
)

func ValidateVMName(name string) error {
	if len(name) == 0 || len(name) > 63 {
		return fmt.Errorf("invalid VM name %q: must be 1-63 characters", name)
	}
	if !vmNamePattern.MatchString(name) {
		return fmt.Errorf("invalid VM name %q: must be a lowercase RFC 1123 DNS label (letters, digits, hyphens)", name)
	}
	return nil
}

func ValidateGroupName(name string) error {
	if !groupNamePattern.MatchString(name) {
		return fmt.Errorf("invalid group name %q: must start with letter/digit and contain only letters, digits, spaces, hyphens, and underscores (max 63 chars)", name)
	}
	return nil
}

func ValidateProfileID(id string) error {
	if !profileIDPattern.MatchString(id) {
		return fmt.Errorf("invalid profile id %q: must start with letter/digit and contain only letters, digits, hyphens, and underscores (max 63 chars)", id)
	}
	return nil
}

func ValidatePlaybookFilename(name string) error {
	if !playbookFilePattern.MatchString(name) {
		return fmt.Errorf("invalid playbook filename %q: must end in .yml or .yaml and contain only letters, digits, hyphens, and underscores", name)
	}
	return nil
}
