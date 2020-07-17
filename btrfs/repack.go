package btrfs

import (
	"fmt"
	"os/exec"
	"path"

	"github.com/anuvu/stacker/container"
)

func (b *btrfs) Repack(ociDir, name, layerType string) error {
	fmt.Println("in repack subcommand")
	fmt.Println(ociDir)
	fmt.Println(b.c.OCIDir)
	content, _ := exec.Command("ls", "-al", path.Join(ociDir, "blobs", "sha256")).CombinedOutput()
	fmt.Println("cached blobs", string(content))
	content, _ = exec.Command("ls", "-al", path.Join(b.c.OCIDir, "blobs", "sha256")).CombinedOutput()
	fmt.Println("cached blobs", string(content))
	return container.RunUmociSubcommand(b.c, []string{
		"--oci-path", ociDir,
		"--tag", name,
		"--bundle-path", path.Join(b.c.RootFSDir, name),
		"repack",
		"--layer-type", layerType,
	})
}
