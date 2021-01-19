package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	stackeroci "github.com/anuvu/stacker/oci"
	"github.com/dustin/go-humanize"
	"github.com/opencontainers/go-digest"
	ispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/opencontainers/umoci"
	"github.com/opencontainers/umoci/oci/casext"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var inspectCmd = cli.Command{
	Name:   "inspect",
	Usage:  "print the json representation of an OCI image",
	Action: doInspect,
	Flags:  []cli.Flag{
		cli.BoolFlag{
			Name:  "show-config",
			Usage: "show the runtime config of the container",
		},
		cli.BoolFlag{
			Name:  "show-annotations",
			Usage: "show the image metadata annotations",
		},
	},
	ArgsUsage: `[tag]

<tag> is the tag in the stackerfile to inspect. If none is supplied, inspect
prints the information on all tags.`,
}

type digestToLayers map[digest.Digest][]string

func (m digestToLayers) Add(oci casext.Engine, tag string) error {
	man, err := stackeroci.LookupManifest(oci, tag)
	if err != nil {
		return err
	}

	for _, l := range man.Layers {
		m[l.Digest] = append(m[l.Digest], tag)
	}

	return nil
}

func rawRender(layers []string) string {
	return strings.Join(layers, ", ")
}

func (m digestToLayers) maxLen() int {
	max := 0
	for d, _ := range m {
		l := len(rawRender(m[d]))
		if l > max {
			max = l
		}
	}

	return max
}

func (m digestToLayers) Render(d digest.Digest) string {
	layers := m[d]
	return fmt.Sprintf("%*s", m.maxLen(), rawRender(layers))
}

func doInspect(ctx *cli.Context) error {
	oci, err := umoci.OpenLayout(config.OCIDir)
	if err != nil {
		return err
	}
	defer oci.Close()

	tags, err := oci.ListReferences(context.Background())
	if err != nil {
		return err
	}

	dtl := digestToLayers{}
	for _, t := range tags {
		fmt.Println("adding tag", t)
		err = dtl.Add(oci, t)
		if err != nil {
			return err
		}
	}

	arg := ctx.Args().Get(0)
	if arg != "" {
		return renderManifest(ctx, oci, dtl, arg)
	}

	for _, t := range tags {
		err = renderManifest(ctx, oci, dtl, t)
		if err != nil {
			return err
		}
	}

	return nil
}

func renderManifest(ctx *cli.Context, oci casext.Engine, dtl digestToLayers, name string) error {
	man, err := stackeroci.LookupManifest(oci, name)
	if err != nil {
		return err
	}

	fmt.Printf("%s\n", name)
	for i, l := range man.Layers {
		humanHash := l.Digest.Encoded()[:12]
		humanSize := humanize.Bytes(uint64(l.Size))
		layerNames := dtl.Render(l.Digest)
		fmt.Printf("\tlayer %d: %s %s... (%s, %s)\n", i, layerNames, humanHash, humanSize, l.MediaType)
	}

	if !ctx.Bool("show-annotations") {
		return nil
	}

	if len(man.Annotations) > 0 {
		fmt.Printf("Annotations:\n")
		for k, v := range man.Annotations {
			fmt.Printf("  %s: %s\n", k, v)
		}
	}

	configBlob, err := oci.FromDescriptor(context.Background(), man.Config)
	if err != nil {
		return err
	}

	if configBlob.Descriptor.MediaType != ispec.MediaTypeImageConfig {
		return errors.Errorf("bad image config type: %s", configBlob.Descriptor.MediaType)
	}

	config := configBlob.Data.(ispec.Image)

	if !ctx.Bool("show-config") {
		return nil
	}

	fmt.Printf("Image config:\n")
	pretty, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(pretty))
	return nil
}
