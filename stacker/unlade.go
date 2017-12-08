package main

import (
	"fmt"
	"io"

	"github.com/anuvu/stacker"
	"github.com/openSUSE/umoci"
	"github.com/urfave/cli"
	ispec "github.com/opencontainers/image-spec/specs-go/v1"
)

var unladeCmd = cli.Command{
	Name: "unlade",
	Usage: "unpacks an OCI image to a directory",
	Action: doUnlade,
	Flags: []cli.Flag{},
}

func doUnlade(ctx *cli.Context) error {
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

	for _, tag := range tags {
		blobs, err := oci.LayersForTag(tag)
		if err != nil {
			return err
		}

		for _, b := range blobs {
			defer b.Close()
		}

		for _, b := range blobs {
			if b.MediaType != ispec.MediaTypeImageLayer {
				return fmt.Errorf("bad blob type %s", b.MediaType)
			}

			reader, ok := b.Data.(io.ReadCloser)
			if !ok {
				return fmt.Errorf("couldn't cast blob data to reader")
			}

			defer reader.Close()

			err = s.Undiff(stacker.NativeDiff, reader)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
