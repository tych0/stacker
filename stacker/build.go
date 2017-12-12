package main

import (
	"fmt"
	"io"
	"time"
	"runtime"

	"github.com/anuvu/stacker"
	"github.com/openSUSE/umoci"
	"github.com/urfave/cli"
	igen "github.com/openSUSE/umoci/oci/config/generate"
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
		cli.StringFlag{
			Name:  "stacker-file, f",
			Usage: "the input stackerfile",
			Value: "stacker.yaml",
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
	results := map[string]umoci.Layer{}

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

		layer, err := oci.PutBlob(diff)
		if err != nil {
			return err
		}

		fmt.Printf("added blob %s\n", layer)
		results[name] = layer

		deps := []umoci.Layer{layer}
		for cur := l; cur.From.Type == stacker.BuiltType; cur = sf[cur.From.Tag] {
			deps = append([]umoci.Layer{results[cur.From.Tag]}, deps...)
		}

		g := igen.New()
		g.SetCreated(time.Now())
		g.SetOS(runtime.GOOS)
		g.SetArchitecture(runtime.GOARCH)
		g.ClearHistory()

		g.SetRootfsType("layers")
		g.ClearRootfsDiffIDs()

		for _, d := range deps {
			digest, err := d.ToDigest()
			if err != nil {
				return err
			}
			g.AddRootfsDiffID(digest)
		}

		if l.Entrypoint != "" {
			cmd, err := l.ParseEntrypoint()
			if err != nil {
				return err
			}

			g.SetConfigEntrypoint(cmd)
		}

		// TODO: we should probably support setting environment
		// variables somehow, but for now let's set a sane PATH
		g.ClearConfigEnv()
		g.AddConfigEnv("PATH", "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin/bin")

		if err := oci.NewImage(name, g, deps, stacker.MediaTypeImageBtrfsLayer); err != nil {
			return err
		}
	}

	return nil
}
