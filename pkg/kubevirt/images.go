package kubevirt

// FindImages returns the curated list of container-disk images the launch
// flow knows how to wire up. This is a static catalog keyed on the Ubuntu
// release tag — LaunchVM builds the image URL via UbuntuContainerDiskImage.
// Once DataVolume-backed disks land, this grows a `remote` field pointing
// at an HTTP source.
func (c *kubevirtClient) FindImages() ([]ImageInfo, error) {
	return []ImageInfo{
		{
			Name:    "24.04",
			Aliases: []string{"lts", "noble"},
			OS:      "Ubuntu",
			Release: "24.04 LTS (Noble Numbat)",
			Remote:  UbuntuContainerDiskImage("24.04"),
			Type:    "image",
		},
		{
			Name:    "22.04",
			Aliases: []string{"jammy"},
			OS:      "Ubuntu",
			Release: "22.04 LTS (Jammy Jellyfish)",
			Remote:  UbuntuContainerDiskImage("22.04"),
			Type:    "image",
		},
	}, nil
}
