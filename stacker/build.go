package main

import (
	"fmt"
	"io"

	"github.com/anuvu/stacker"
	"github.com/openSUSE/umoci"
	"github.com/urfave/cli"
)

var buildCmd = cli.Command{
	Name:   "build",
	Usage:  "builds a new OCI image from a stacker yaml file",
	Action: doBuild,
	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "leave-unladen",
			Usage: "leave the built rootfs mount after image building",
		},
	},
}

func doBuild(ctx *cli.Context) error {
	file := ctx.String("f")
	sf, err := stacker.NewStackerfile(file)
	if err != nil {
		return err
	}

	s, err := stacker.NewStorage(config)
	if err != nil {
		return err
	}
	if !ctx.Bool("leave-unladen") {
		defer s.Detach()
	}

	order, err := sf.DependencyOrder()
	if err != nil {
		return err
	}

	oci, err := umoci.CreateLayout(config.OCIDir)
	if err != nil {
		return err
	}

	defer s.Delete("working")
	results := map[string]string{}

	for _, name := range order {
		l := sf[name]

		s.Delete(".working")
		fmt.Printf("building image %s...\n", name)
		if l.From.Type == stacker.BuiltType {
			if err := s.Restore(l.From.Tag, ".working"); err != nil {
				return err
			}
		} else {
			if err := s.Create(".working"); err != nil {
				return err
			}

			err := stacker.GetBaseLayer(config, ".working", l)
			if err != nil {
				return err
			}
		}

		fmt.Println("importing files...")
		if err := stacker.Import(config, name, l.Import); err != nil {
			return err
		}

		fmt.Println("running commands...")
		if err := stacker.Run(config, name, l.Run); err != nil {
			return err
		}

		if err := s.Snapshot(".working", name); err != nil {
			return err
		}
		fmt.Printf("filesystem %s built successfully\n", name)

		var diff io.Reader
		if l.From.Type == stacker.BuiltType {
			diff, err = s.Diff(stacker.NativeDiff, l.From.Tag, name)
			if err != nil {
				return err
			}
		} else {
			diff, err = s.Diff(stacker.NativeDiff, "", name)
			if err != nil {
				return err
			}
		}

		hash, err := oci.AddBlob(diff)
		if err != nil {
			return err
		}
		fmt.Printf("added blob %s", hash)

		deps := []string{}
		for cur := l; cur.From.Type == stacker.BuiltType; cur = sf[cur.From.Tag] {
			if cur.From.Type != stacker.BuiltType {
				break
			}

			deps = append([]string{results[cur.From.Tag]}, deps...)
		}

		if err := oci.NewImage(name, deps); err != nil {
			return err
		}
	}

	return nil
}
