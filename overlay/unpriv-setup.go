package overlay

import (
	"fmt"
	"os"
	"os/user"

	"github.com/anuvu/stacker/container"
	"github.com/anuvu/stacker/types"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

func UnprivSetup(config types.StackerConfig, uid, gid int) error {
	err := unix.Setuid(uid)
	if err != nil {
		return errors.Wrapf(err, "problem dropping uid")
	}

	err = unix.Setgid(gid)
	if err != nil {
		return errors.Wrapf(err, "problem dropping gid")
	}

	// all we really care about is that we can do unpriv mounts and create
	// whiteouts; there's no setup to do outside of that. So just check
	// that we can do those.
	u, err := user.LookupId(fmt.Sprintf("%d", uid))
	if err != nil {
		return errors.Wrapf(err, "couldn't find user for %d", uid)
	}

	idmapSet, err := container.ResolveIdmapSet(u.Username)
	if err != nil {
		return err
	}

	// TODO: we should rework RunUmociSubcommand so we can use it for this
	// too. But we can leave that for when we rename umoci and do that
	// whole refactoring.
	binary, err := os.Readlink("/proc/self/exe")
	if err != nil {
		return err
	}

	cmd := []string{
		binary,
		"--oci-dir", config.OCIDir,
		"--roots-dir", config.RootFSDir,
		"--stacker-dir", config.StackerDir,
		"--storage-type", config.StorageType,
	}

	if config.Debug {
		cmd = append(cmd, "--debug")
	}

	cmd = append(cmd, "umoci")
	cmd = append(cmd, "check-overlay")

	return container.RunInUserns(idmapSet, cmd, "overlay perms check failed")
}
