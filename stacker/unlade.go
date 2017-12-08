package main

import (
	//"fmt"
	//"io"

	//"github.com/anuvu/stacker"
	//"github.com/openSUSE/umoci"
	"github.com/urfave/cli"
)

var unladeCmd = cli.Command{
	Name: "unlade",
	Usage: "unpacks an OCI image to a directory",
	Action: doUnlade,
	Flags: []cli.Flag{},
}

func doUnlade(ctx *cli.Context) error {
	/*
	file := ctx.String("f")
	sf, err := stacker.NewStackerfile(file)
	if err != nil {
		return err
	}

	s, err := stacker.NewStorage(config)
	if err != nil {
		return err
	}

	oci, err := umoci.OpenLayout(config.OCIDir)
	if err != nil {
		return err
	}

	tags, err := oci.ListTags()
	if err != nil {
		return err
	}
	*/

	return nil
}
