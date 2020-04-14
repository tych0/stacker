package stacker

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path"
	"strings"
	"syscall"

	"github.com/apex/log"
	"github.com/lxc/lxd/shared/idmap"
	"github.com/pkg/errors"
	"gopkg.in/lxc/go-lxc.v2"
)

const (
	ReasonableDefaultPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	WorkingContainerName  = "_working"
)

var (
	IdmapSet *idmap.IdmapSet
)

func init() {
	if os.Geteuid() != 0 {
		currentUser, err := user.Current()
		if err != nil {
			log.Warnf("failed getting current user: %v", err)
			return
		}

		// An error here means that this user has no subuid
		// delegations. The only thing we can do is panic, and if we're
		// re-execing inside a user namespace we don't want to do that.
		// So let's just ignore the error and let future code handle it.
		IdmapSet, err = idmap.DefaultIdmapSet("", currentUser.Username)
		if err != nil {
			log.Warnf("failed parsing /etc/sub{u,g}idmap: %v", err)
		}

		if IdmapSet != nil {
			/* Let's make our current user the root user in the ns, so that when
			 * stacker emits files, it does them as the right user.
			 */
			hostMap := []idmap.IdmapEntry{
				idmap.IdmapEntry{
					Isuid:    true,
					Hostid:   int64(os.Getuid()),
					Nsid:     0,
					Maprange: 1,
				},
				idmap.IdmapEntry{
					Isgid:    true,
					Hostid:   int64(os.Getgid()),
					Nsid:     0,
					Maprange: 1,
				},
			}

			for _, hm := range hostMap {
				err := IdmapSet.AddSafe(hm)
				if err != nil {
					log.Warnf("Failed adding idmap entry: %v", err)
					return
				}
			}
		}
	}
}

// our representation of a container
type Container struct {
	sc StackerConfig
	c  *lxc.Container
}

func NewContainer(sc StackerConfig, name string) (*Container, error) {
	if !lxc.VersionAtLeast(2, 1, 0) {
		return nil, fmt.Errorf("stacker requires liblxc >= 2.1.0")
	}

	lxcC, err := lxc.NewContainer(name, sc.RootFSDir)
	if err != nil {
		return nil, err
	}
	c := &Container{sc: sc, c: lxcC}

	if err := c.c.SetLogLevel(lxc.TRACE); err != nil {
		return nil, err
	}

	logFile := path.Join(sc.StackerDir, "lxc.log")
	err = c.c.SetLogFile(logFile)
	if err != nil {
		return nil, err
	}

	// Truncate the log file by hand, so people don't get confused by
	// previous runs.
	err = os.Truncate(logFile, 0)
	if err != nil {
		return nil, err
	}

	if IdmapSet != nil {
		for _, idm := range IdmapSet.Idmap {
			if err := idm.Usable(); err != nil {
				return nil, fmt.Errorf("idmap unusable: %s", err)
			}
		}

		for _, lxcConfig := range IdmapSet.ToLxcString() {
			err = c.setConfig("lxc.idmap", lxcConfig)
			if err != nil {
				return nil, err
			}
		}

		// If we're in a userns, we need to be sure and make sure the
		// rootfs pivot dir is somewhere that we can actually write to.
		// Let's use .stacker/rootfs instead of /var/lib/lxc/rootfs
		rootfsPivot := path.Join(sc.StackerDir, "rootfsPivot")
		if err := os.MkdirAll(rootfsPivot, 0755); err != nil {
			return nil, err
		}

		if err := c.setConfig("lxc.rootfs.mount", rootfsPivot); err != nil {
			return nil, err
		}
	}

	configs := map[string]string{
		"lxc.mount.auto":  "proc:mixed",
		"lxc.autodev":     "1",
		"lxc.pty.max":     "1024",
		"lxc.mount.entry": "none dev/shm tmpfs defaults,create=dir 0 0",
		"lxc.uts.name":    name,
		"lxc.net.0.type":  "none",
		"lxc.environment": fmt.Sprintf("PATH=%s", ReasonableDefaultPath),
	}

	if err := c.setConfigs(configs); err != nil {
		return nil, err
	}

	err = c.bindMount("/sys", "/sys", "")
	if err != nil {
		return nil, err
	}

	err = c.bindMount("/etc/resolv.conf", "/etc/resolv.conf", "")
	if err != nil {
		return nil, err
	}

	rootfs := path.Join(sc.RootFSDir, name, "rootfs")
	err = c.setConfig("lxc.rootfs.path", fmt.Sprintf("dir:%s", rootfs))
	if err != nil {
		return nil, err
	}

	return c, nil
}

func (c *Container) bindMount(source string, dest string, extraOpts string) error {
	createOpt := "create=dir"
	stat, err := os.Stat(source)
	if err == nil && !stat.IsDir() {
		createOpt = "create=file"
	}

	val := fmt.Sprintf("%s %s none rbind,%s,%s", source, strings.TrimPrefix(dest, "/"), createOpt, extraOpts)
	return c.setConfig("lxc.mount.entry", val)
}

func (c *Container) setConfigs(config map[string]string) error {
	for k, v := range config {
		if err := c.setConfig(k, v); err != nil {
			return err
		}
	}

	return nil
}

func (c *Container) setConfig(name string, value string) error {
	err := c.c.SetConfigItem(name, value)
	if err != nil {
		return fmt.Errorf("failed setting config %s to %s: %v", name, value, err)
	}
	return nil
}

// containerError tries its best to report as much context about an LXC error
// as possible.
func (c *Container) containerError(theErr error, msg string) error {
	if theErr == nil {
		return nil
	}

	f, err := os.Open(c.c.LogFile())
	if err != nil {
		return errors.Wrap(theErr, msg)
	}

	lxcErrors := []string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "ERROR") {
			lxcErrors = append(lxcErrors, line)
		}
	}

	for _, err := range lxcErrors {
		log.Debug(err)
	}
	return theErr
}

func (c *Container) Execute(args string, stdin io.Reader) error {
	if err := c.setConfig("lxc.execute.cmd", args); err != nil {
		return err
	}

	f, err := ioutil.TempFile("", fmt.Sprintf("stacker_%s_run", c.c.Name()))
	if err != nil {
		return err
	}
	f.Close()
	defer os.Remove(f.Name())

	if err := c.c.SaveConfigFile(f.Name()); err != nil {
		return err
	}

	// we want to be sure to remove the /stacker from the generated
	// filesystem after execution.
	defer os.Remove(path.Join(c.sc.RootFSDir, c.c.Name(), "rootfs", "stacker"))

	// Just in case the binary has chdir'd somewhere since it started,
	// let's readlink /proc/self/exe to figure out what to exec.
	binary, err := os.Readlink("/proc/self/exe")
	if err != nil {
		return err
	}

	cmd := exec.Command(
		binary,
		"internal",
		c.c.Name(),
		c.sc.RootFSDir,
		f.Name(),
	)

	cmd.Stdin = stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// If this is non-interactive, we're going to setsid() later, so we
	// need to make sure we capture the output somehow.
	if stdin == nil {
		reader, writer := io.Pipe()
		defer writer.Close()

		cmd.Stdout = writer
		cmd.Stderr = writer

		go func() {
			defer reader.Close()
			_, err := io.Copy(os.Stdout, reader)
			if err != nil {
				fmt.Println("err from stdout copy:", err)
			}
		}()

	}

	signals := make(chan os.Signal)
	signal.Notify(signals)
	done := make(chan bool)

	go func() {
		for {
			select {
			case <-done:
				return
			case sg := <-signals:
				// ignore SIGCHLD, we can't forward it and it's
				// meaningless anyway
				if sg == syscall.SIGCHLD {
					continue
				}

				// upgrade SIGINT to SIGKILL. bash exits when
				// it receives SIGINT, but doesn't kill its
				// children, meaning the currently executing
				// command will keep executing until it
				// completes, and *then* things will die.
				// Instead, let's just force kill it.
				if sg == syscall.SIGINT {
					sg = syscall.SIGKILL
				}

				err = syscall.Kill(c.c.InitPid(), sg.(syscall.Signal))
				if err != nil {
					fmt.Println("failed to send signal", sg, err)
				}
			}
		}
	}()

	cmdErr := cmd.Run()
	done <- true

	return c.containerError(cmdErr, "execute failed")
}

func (c *Container) SetupLayerConfig(realContainerName string, l *Layer) error {
	env, err := l.BuildEnvironment()
	if err != nil {
		return err
	}

	// this is a bit of a hack, we can't use c.c.Name() because during a
	// build, we use WorkingContainerName. Seems like maybe we should just
	// get rid of the working contianer all together and just build the
	// thing wherever it will finally live, but that's a fairly major
	// refactoring.
	importsDir := path.Join(c.sc.StackerDir, "imports", realContainerName)
	if _, err := os.Stat(importsDir); err == nil {
		err = c.bindMount(importsDir, "/stacker", "ro")
		if err != nil {
			return err
		}
	}

	for k, v := range env {
		if v != "" {
			err = c.setConfig("lxc.environment", fmt.Sprintf("%s=%s", k, v))
			if err != nil {
				return err
			}
		}
	}

	binds, err := l.ParseBinds()
	if err != nil {
		return err
	}

	for source, target := range binds {
		err = c.bindMount(source, target, "")
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *Container) Close() {
	c.c.Release()
}

func RunInUserns(userCmd []string, msg string) error {
	if IdmapSet == nil {
		return errors.Errorf("no subuids!")
	}

	args := []string{}

	for _, idm := range IdmapSet.Idmap {
		var which string
		if idm.Isuid && idm.Isgid {
			which = "b"
		} else if idm.Isuid {
			which = "u"
		} else if idm.Isgid {
			which = "g"
		}

		m := fmt.Sprintf("%s:%d:%d:%d", which, idm.Nsid, idm.Hostid, idm.Maprange)
		args = append(args, "-m", m)
	}

	args = append(args, "--")
	args = append(args, userCmd...)

	cmd := exec.Command("lxc-usernsexec", args...)

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("error %s: %s", msg, err)
	}

	return nil
}

// A wrapper which runs things in a userns if we're an unprivileged user with
// an idmap, or runs things on the host if we're root and don't.
func MaybeRunInUserns(userCmd []string, msg string) error {
	if IdmapSet == nil {
		if os.Geteuid() != 0 {
			return fmt.Errorf("no idmap and not root, can't run %v", userCmd)
		}

		cmd := exec.Command(userCmd[0], userCmd[1:]...)
		cmd.Stdin = nil
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	return RunInUserns(userCmd, msg)

}

// GenerateShellForRunning generates a shell script to run inside the
// container, and writes it to the contianer. It does a few additional checks:
// does the script already have a shebang? If so, it leaves it as is, otherwise
// it prepends a shebang. It also makes sure the rootfs has a shell.
func GenerateShellForRunning(rootfs string, cmd []string, outFile string) error {
	shebangLine := "#!/bin/sh -xe\n"
	if strings.HasPrefix(cmd[0], "#!") {
		shebangLine = ""
	} else {
		_, err := os.Stat(path.Join(rootfs, "bin/sh"))
		if err != nil {
			return errors.Errorf("rootfs %s does not have a /bin/sh", rootfs)
		}
	}

	return ioutil.WriteFile(outFile, []byte(shebangLine+strings.Join(cmd, "\n")+"\n"), 0755)
}
