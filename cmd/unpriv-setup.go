package main

import (
	"os"
	"strconv"

	"github.com/anuvu/stacker"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var unprivSetupCmd = cli.Command{
	Name:   "unpriv-setup",
	Usage:  "do the necessary unprivileged setup for stacker build to work without root",
	Action: doUnprivSetup,
	Before: beforeUnprivSetup,
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "uid",
			Usage: "the user to do setup for (defaults to $SUDO_UID from env)",
			Value: os.Getenv("SUDO_UID"),
		},
		cli.StringFlag{
			Name:  "gid",
			Usage: "the group to do setup for (defaults to $SUDO_GID from env)",
			Value: os.Getenv("SUDO_GID"),
		},
	},
}

func beforeUnprivSetup(ctx *cli.Context) error {
	if ctx.String("uid") == "" {
		return errors.Errorf("please specify --uid or run unpriv-setup with sudo")
	}

	if ctx.String("gid") == "" {
		return errors.Errorf("please specify --gid or run unpriv-setup with sudo")
	}

	return nil
}

func doUnprivSetup(ctx *cli.Context) error {
	_, err := os.Stat(config.StackerDir)
	if err == nil {
		return errors.Errorf("stacker dir %s already exists, aborting setup", config.StackerDir)
	}

	uid, err := strconv.Atoi(ctx.String("uid"))
	if err != nil {
		return errors.Wrapf(err, "couldn't convert uid %s", ctx.String("uid"))
	}

	gid, err := strconv.Atoi(ctx.String("gid"))
	if err != nil {
		return errors.Wrapf(err, "couldn't convert gid %s", ctx.String("gid"))
	}

	return stacker.UnprivSetup(config, uid, gid)
}
