package kubevirt

// catalogEntry is one row of the curated container-disk list. `Distro` is
// the package-manager family used by the qemu-guest-agent injector:
// "debian" (apt), "rhel" (dnf). Alpine is omitted because containerdisks
// does not publish a maintained alpine image and Alpine's tiny-cloud-init
// doesn't honour the same directives.
type catalogEntry struct {
	Name    string
	OS      string
	Release string
	Aliases []string
	Distro  string
	Image   string
}

var imageCatalog = []catalogEntry{
	{Name: "24.04", OS: "Ubuntu", Release: "24.04 LTS (Noble Numbat)", Aliases: []string{"lts", "noble"}, Distro: "debian", Image: "quay.io/containerdisks/ubuntu:24.04"},
	{Name: "22.04", OS: "Ubuntu", Release: "22.04 LTS (Jammy Jellyfish)", Aliases: []string{"jammy"}, Distro: "debian", Image: "quay.io/containerdisks/ubuntu:22.04"},
	{Name: "20.04", OS: "Ubuntu", Release: "20.04 LTS (Focal Fossa)", Aliases: []string{"focal"}, Distro: "debian", Image: "quay.io/containerdisks/ubuntu:20.04"},
	{Name: "daily", OS: "Ubuntu", Release: "Daily build", Aliases: []string{"devel"}, Distro: "debian", Image: "quay.io/containerdisks/ubuntu:daily"},
	{Name: "debian-12", OS: "Debian", Release: "12 (Bookworm)", Aliases: []string{"bookworm"}, Distro: "debian", Image: "quay.io/containerdisks/debian:12"},
	{Name: "fedora-40", OS: "Fedora", Release: "40", Distro: "rhel", Image: "quay.io/containerdisks/fedora:40"},
	{Name: "centos-stream-9", OS: "CentOS Stream", Release: "9", Aliases: []string{"c9s"}, Distro: "rhel", Image: "quay.io/containerdisks/centos-stream:9"},
	{Name: "rockylinux-9", OS: "Rocky Linux", Release: "9", Aliases: []string{"rocky"}, Distro: "rhel", Image: "quay.io/containerdisks/rockylinux:9"},
}

// imageByName resolves an image name or alias to its catalog entry.
// Empty name maps to the default Ubuntu LTS. Unknown names return nil —
// callers that need a URL fall through to treating the value as an Ubuntu
// release tag for back-compat with pre-catalog profiles.
func imageByName(name string) *catalogEntry {
	if name == "" {
		name = DefaultUbuntuRelease
	}
	for i := range imageCatalog {
		if imageCatalog[i].Name == name {
			return &imageCatalog[i]
		}
		for _, a := range imageCatalog[i].Aliases {
			if a == name {
				return &imageCatalog[i]
			}
		}
	}
	return nil
}

// ContainerDiskImage maps a catalog name (or alias, or legacy Ubuntu release
// tag) to a containerdisk registry URL. Unknown names fall through to Ubuntu
// so profiles created before the multi-OS catalog keep working.
func ContainerDiskImage(name string) string {
	if e := imageByName(name); e != nil {
		return e.Image
	}
	return "quay.io/containerdisks/ubuntu:" + name
}

// ImageDistro returns the package-manager family for a catalog name:
// "debian" (apt), "rhel" (dnf), or "" when unknown. Used by the cloud-init
// qemu-guest-agent injector to pick the right install command.
func ImageDistro(name string) string {
	if e := imageByName(name); e != nil {
		return e.Distro
	}
	// Unknown names are assumed to be Ubuntu release tags (back-compat).
	return "debian"
}

// UbuntuContainerDiskImage is retained for back-compat; new code should use
// ContainerDiskImage.
func UbuntuContainerDiskImage(release string) string {
	if release == "" {
		release = DefaultUbuntuRelease
	}
	return "quay.io/containerdisks/ubuntu:" + release
}

// FindImages returns the curated container-disk catalog for the launch flow.
func (c *kubevirtClient) FindImages() ([]ImageInfo, error) {
	out := make([]ImageInfo, 0, len(imageCatalog))
	for _, e := range imageCatalog {
		out = append(out, ImageInfo{
			Name:    e.Name,
			Aliases: e.Aliases,
			OS:      e.OS,
			Release: e.Release,
			Remote:  e.Image,
			Type:    "image",
		})
	}
	return out, nil
}
