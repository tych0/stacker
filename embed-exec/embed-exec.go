// This is a package to go from an embed.FS + file name to an exec.Command;
// works only on recent linux kernels
package embed_exec

import (
	"embed"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"

	// "github.com/justincormack/go-memfd"
	"github.com/pkg/errors"
)

func GetCommand(fs embed.FS, filename string, args ...string) (*exec.Cmd, func() error, error) {
	f, err := fs.Open(filename)
	if err != nil {
		return &exec.Cmd{}, nil, errors.WithStack(err)
	}
	defer f.Close()

	mfd, err := ioutil.TempFile("", fmt.Sprintf("embed-exec-%s", filename))
	if err != nil {
		return &exec.Cmd{}, nil, errors.WithStack(err)
	}
	defer mfd.Close()
	defer os.Remove(mfd.Name())

	err = mfd.Chmod(0777)
	if err != nil {
		return &exec.Cmd{}, nil, errors.WithStack(err)
	}

	_, err = io.Copy(mfd, f)
	if err != nil {
		mfd.Close()
		return &exec.Cmd{}, nil, errors.WithStack(err)
	}

	cmd := exec.Command(mfd.Name(), args...)
	return cmd, mfd.Close, nil
}
