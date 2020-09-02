package overlay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path"

	"github.com/anuvu/stacker/log"
	stackeroci "github.com/anuvu/stacker/oci"
	"github.com/anuvu/stacker/types"
	//"github.com/opencontainers/go-digest"
	ispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/opencontainers/umoci/oci/casext"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

type overlayMetadata struct {
	Manifests map[types.LayerType]ispec.Manifest

	// layers not yet rendered into the output image
	BuiltLayers []string
}

func newOverlayMetadata() overlayMetadata {
	return overlayMetadata{Manifests: map[types.LayerType]ispec.Manifest{}}
}

func newOverlayMetadataFromOCI(oci casext.Engine, tag string) (overlayMetadata, error) {
	ovl := newOverlayMetadata()
	var err error

	manifest, err := stackeroci.LookupManifest(oci, tag)
	if err != nil {
		return overlayMetadata{}, err
	}

	layerType, err := types.NewLayerTypeManifest(manifest)
	if err != nil {
		return overlayMetadata{}, err
	}

	ovl.Manifests[layerType] = manifest
	return ovl, nil
}

func readOverlayMetadata(config types.StackerConfig, tag string) (overlayMetadata, error) {
	metadataFile := path.Join(config.RootFSDir, tag, "overlay_metadata.json")
	content, err := ioutil.ReadFile(metadataFile)
	if err != nil {
		return overlayMetadata{}, errors.Wrapf(err, "couldn't read overlay metadata %s", metadataFile)
	}

	var ovl overlayMetadata
	err = json.Unmarshal(content, &ovl)
	if err != nil {
		return overlayMetadata{}, errors.Wrapf(err, "couldnt' unmarshal overlay metadata %s", metadataFile)
	}

	return ovl, err
}

func (ovl overlayMetadata) write(config types.StackerConfig, tag string) error {
	content, err := json.Marshal(&ovl)
	if err != nil {
		return errors.Wrapf(err, "couldn't marshal overlay metadata")
	}

	metadataFile := path.Join(config.RootFSDir, tag, "overlay_metadata.json")
	err = ioutil.WriteFile(metadataFile, content, 0644)
	if err != nil {
		return errors.Wrapf(err, "couldn't write overlay metadata %s", metadataFile)
	}

	return nil
}

func (ovl overlayMetadata) mount(config types.StackerConfig, tag string) error {
	overlayArgs := bytes.NewBufferString("index=off,lowerdir=")

	// find *any* manifest to mount: we don't care if this is tar or
	// squashfs, we just need to mount something. the code that generates
	// the output needs to care about this, not this code.
	//
	// if there are no manifests (this came from a tar layer or whatever),
	// that's fine too; we just end up with two workaround directories as
	// below
	var manifest ispec.Manifest
	for _, m := range ovl.Manifests {
		manifest = m
		break
	}

	for _, layer := range manifest.Layers {
		contents := overlayPath(config, layer.Digest, "overlay")
		if _, err := os.Stat(contents); err != nil {
			return errors.Wrapf(err, "%s does not exist", contents)
		}
		overlayArgs.WriteString(contents)
		overlayArgs.WriteString(":")
	}

	for _, layer := range ovl.BuiltLayers {
		contents := path.Join(config.RootFSDir, layer, "overlay")
		if _, err := os.Stat(contents); err != nil {
			return errors.Wrapf(err, "%s does not exist", contents)
		}
		overlayArgs.WriteString(contents)
		overlayArgs.WriteString(":")
	}

	if len(manifest.Layers)+len(ovl.BuiltLayers) < 2 {
		// overlayfs doesn't work with < 2 lowerdirs, so we add some
		// workaround dirs if necessary (if e.g. the source only has
		// one layer, or it's an empty rootfs with no layers, we still
		// want an overlay mount to keep things consistent)

		for i := 0; i < 2-len(manifest.Layers)+len(ovl.BuiltLayers); i++ {
			workaround := path.Join(config.RootFSDir, tag, fmt.Sprintf("workaround%d", i))
			err := os.MkdirAll(workaround, 0755)
			if err != nil {
				return errors.Wrapf(err, "couldn't make workaround dir")
			}

			overlayArgs.WriteString(workaround)
			overlayArgs.WriteString(":")
		}

	}

	// chop off the last : from lowerdir= above
	overlayArgs.Truncate(overlayArgs.Len() - 1)

	overlayArgs.WriteString(",")

	overlayArgs.WriteString("upperdir=")
	overlayArgs.WriteString(path.Join(config.RootFSDir, tag, "overlay"))
	overlayArgs.WriteString(",")

	overlayArgs.WriteString("workdir=")
	overlayArgs.WriteString(path.Join(config.RootFSDir, tag, "work"))

	rootfs := path.Join(config.RootFSDir, tag, "rootfs")
	log.Debugf("mount overlay args %s", overlayArgs.String())
	err := unix.Mount("overlay", rootfs, "overlay", 0, overlayArgs.String())
	return errors.Wrapf(err, "failed to mount overlay for %s", tag)
}
