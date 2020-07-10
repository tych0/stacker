package stacker

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"time"

	"github.com/anuvu/stacker/container"
	"github.com/anuvu/stacker/lib"
	"github.com/anuvu/stacker/log"
	stackeroci "github.com/anuvu/stacker/oci"
	"github.com/anuvu/stacker/squashfs"
	"github.com/anuvu/stacker/types"
	"github.com/klauspost/pgzip"
	"github.com/opencontainers/go-digest"
	ispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/opencontainers/umoci"
	"github.com/opencontainers/umoci/oci/casext"
	"github.com/opencontainers/umoci/oci/layer"
	"github.com/pkg/errors"
)

type BaseLayerOpts struct {
	Config    types.StackerConfig
	Name      string
	Layer     *types.Layer
	Cache     *BuildCache
	OCI       casext.Engine
	LayerType string
	Storage   types.Storage
	Progress  bool
}

// GetBase grabs the base layer and puts it in the cache.
func GetBase(o BaseLayerOpts) error {
	switch o.Layer.From.Type {
	case types.BuiltLayer:
		return nil
	case types.ScratchLayer:
		return nil
	case types.TarLayer:
		cacheDir := path.Join(o.Config.StackerDir, "layer-bases")
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			return err
		}

		_, err := acquireUrl(o.Config, o.Layer.From.Url, cacheDir, o.Progress)
		return err
	/* now we can do all the containers/image types */
	case types.OCILayer:
		fallthrough
	case types.DockerLayer:
		return importContainersImage(o.Layer.From, o.Config, o.Progress)
	default:
		return errors.Errorf("unknown layer type: %v", o.Layer.From.Type)
	}
}

func umociInit(o BaseLayerOpts) error {
	return container.RunUmociSubcommand(o.Config, []string{
		"--tag", o.Name,
		"--oci-path", o.Config.OCIDir,
		"--bundle-path", path.Join(o.Config.RootFSDir, o.Name),
		"init",
	})
}

// SetupRootfs assumes the base layer is correct in the cache, and sets up
// the filesystem image in RootsDir, and copies whatever OCI layers exist for
// the base to the output. If no OCI layers exist for the base (e.g. "scratch"
// or "tar" types), this operation initializes an empty tag in the output.
//
// This will also do the conversion between squashfs and tar imports if
// necessary, so the layers that appear in the output will be of the right
// type.
//
// Finally, if the layer is a build only layer, this code simply initializes
// the filesystem in roots to the built tag's filesystem.
func SetupRootfs(o BaseLayerOpts, sfm types.StackerFiles) error {
	o.Storage.Delete(o.Name)
	if o.Layer.From.Type == types.BuiltLayer {
		// For built type images, we already have the base fs content
		// and umoci metadata. So let's just use that, and copy
		// whatever we can to the output image.
		if err := o.Storage.Restore(o.Layer.From.Tag, o.Name); err != nil {
			return err
		}

		return copyBuiltTypeBaseToOutput(o, sfm)
	}

	// For everything else, we create a new snapshot and extract whatever
	// we can on top of it.
	if err := o.Storage.Create(o.Name); err != nil {
		return err
	}

	switch o.Layer.From.Type {
	case types.TarLayer:
		err := umociInit(o)
		if err != nil {
			return err
		}

		err = o.Storage.SetupEmptyRootfs(o.Name)
		if err != nil {
			return err
		}
		return setupTarRootfs(o)
	case types.ScratchLayer:
		err := umociInit(o)
		if err != nil {
			return err
		}

		return o.Storage.SetupEmptyRootfs(o.Name)
	case types.OCILayer:
		fallthrough
	case types.DockerLayer:
		return setupContainersImageRootfs(o)
	default:
		return errors.Errorf("unknown layer type: %v", o.Layer.From.Type)
	}
}

func importContainersImage(is *types.ImageSource, config types.StackerConfig, progress bool) error {
	toImport, err := is.ContainersImageURL()
	if err != nil {
		return err
	}

	tag, err := is.ParseTag()
	if err != nil {
		return err
	}

	// Note that we can do this over the top of the cache every time, since
	// skopeo should be smart enough to only copy layers that have changed.
	cacheDir := path.Join(config.StackerDir, "layer-bases", "oci")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err
	}

	defer func() {
		oci, err := umoci.OpenLayout(cacheDir)
		if err != nil {
			// Some error might have occurred, in which case we
			// don't have a valid OCI layout, which is fine.
			return
		}
		defer oci.Close()
	}()

	var progressWriter io.Writer
	if progress {
		progressWriter = os.Stderr
	}

	log.Infof("loading %s", toImport)
	err = lib.ImageCopy(lib.ImageCopyOpts{
		Src:      toImport,
		Dest:     fmt.Sprintf("oci:%s:%s", cacheDir, tag),
		SkipTLS:  is.Insecure,
		Progress: progressWriter,
	})
	if err != nil {
		return errors.Wrapf(err, "couldn't import base layer %s", tag)
	}

	return err
}

func setupContainersImageRootfs(o BaseLayerOpts) error {
	target := path.Join(o.Config.RootFSDir, o.Name)
	log.Debugf("unpacking to %s", target)

	cacheDir := path.Join(o.Config.StackerDir, "layer-bases", "oci")
	cacheTag, err := o.Layer.From.ParseTag()
	if err != nil {
		return err
	}

	cacheOCI, err := umoci.OpenLayout(cacheDir)
	if err != nil {
		return err
	}

	sourceLayerType := "tar"
	manifest, err := stackeroci.LookupManifest(cacheOCI, cacheTag)
	if err != nil {
		return err
	}

	if manifest.Layers[0].MediaType == stackeroci.MediaTypeLayerSquashfs {
		sourceLayerType = "squashfs"
	}

	err = o.Storage.Unpack(cacheDir, cacheTag, o.Name)
	if err != nil {
		return err
	}

	if o.Layer.BuildOnly {
		return nil
	}

	// if the layer types are the same, just copy it over and be done
	if o.LayerType == sourceLayerType {
		log.Debugf("same layer type, no translation required")
		// We just copied it to the cache, now let's copy that over to our image.
		err = lib.ImageCopy(lib.ImageCopyOpts{
			Src:  fmt.Sprintf("oci:%s:%s", cacheDir, cacheTag),
			Dest: fmt.Sprintf("oci:%s:%s", o.Config.OCIDir, o.Name),
		})
		return err
	}
	log.Debugf("translating from %s to %s", sourceLayerType, o.LayerType)

	var blob io.ReadCloser

	bundlePath := path.Join(o.Config.RootFSDir, o.Name)
	rootfsPath := path.Join(bundlePath, "rootfs")
	// otherwise, render the right layer type
	if o.LayerType == "squashfs" {
		// sourced a non-squashfs image and wants a squashfs layer,
		// let's generate one.
		o.OCI.GC(context.Background())

		blob, err = squashfs.MakeSquashfs(o.Config.OCIDir, rootfsPath, nil)
		if err != nil {
			return err
		}
		defer blob.Close()
	} else {
		blob = layer.GenerateInsertLayer(path.Join(bundlePath, "rootfs"), "/", false, nil)
		defer blob.Close()
	}

	layerDigest, layerSize, err := o.OCI.PutBlob(context.Background(), blob)
	if err != nil {
		return err
	}

	cacheManifest, err := stackeroci.LookupManifest(cacheOCI, cacheTag)
	if err != nil {
		return err
	}

	config, err := stackeroci.LookupConfig(cacheOCI, cacheManifest.Config)
	if err != nil {
		return err
	}

	layerType := stackeroci.MediaTypeLayerSquashfs
	if o.LayerType == "tar" {
		layerType = ispec.MediaTypeImageLayerGzip
	}

	desc := ispec.Descriptor{
		MediaType: layerType,
		Digest:    layerDigest,
		Size:      layerSize,
	}

	manifest.Layers = []ispec.Descriptor{desc}
	config.RootFS.DiffIDs = []digest.Digest{layerDigest}
	now := time.Now()
	config.History = []ispec.History{{
		Created:   &now,
		CreatedBy: fmt.Sprintf("stacker layer-type mismatch repack of %s", cacheTag),
	},
	}

	configDigest, configSize, err := o.OCI.PutBlobJSON(context.Background(), config)
	if err != nil {
		return err
	}

	manifest.Config = ispec.Descriptor{
		MediaType: ispec.MediaTypeImageConfig,
		Digest:    configDigest,
		Size:      configSize,
	}

	manifestDigest, manifestSize, err := o.OCI.PutBlobJSON(context.Background(), manifest)
	if err != nil {
		return err
	}

	desc = ispec.Descriptor{
		MediaType: ispec.MediaTypeImageManifest,
		Digest:    manifestDigest,
		Size:      manifestSize,
	}

	err = o.OCI.UpdateReference(context.Background(), o.Name, desc)
	if err != nil {
		return err
	}

	return o.Storage.UpdateFSMetadata(o.Name, casext.DescriptorPath{
		Walk: []ispec.Descriptor{desc},
	})
}

func setupTarRootfs(o BaseLayerOpts) error {
	// initialize an empty image, then extract it
	cacheDir := path.Join(o.Config.StackerDir, "layer-bases")
	tar := path.Join(cacheDir, path.Base(o.Layer.From.Url))

	// TODO: make this respect ID maps
	layerPath := path.Join(o.Config.RootFSDir, o.Name, "rootfs")
	tarReader, err := os.Open(tar)
	if err != nil {
		return errors.Wrapf(err, "couldn't open %s", tar)
	}
	defer tarReader.Close()
	var uncompressed io.ReadCloser
	uncompressed, err = pgzip.NewReader(tarReader)
	if err != nil {
		_, err = tarReader.Seek(0, os.SEEK_SET)
		if err != nil {
			return errors.Wrapf(err, "failed to 0 seek %s", tar)
		}
		uncompressed = tarReader
	} else {
		defer uncompressed.Close()
	}

	err = layer.UnpackLayer(layerPath, uncompressed, &layer.UnpackOptions{KeepDirlinks: true})
	if err != nil {
		return err
	}

	return nil
}

func copyBuiltTypeBaseToOutput(o BaseLayerOpts, sfm types.StackerFiles) error {
	// We need to copy any base OCI layers to the output dir, since they
	// may not have been copied before and the final `umoci repack` expects
	// them to be there.
	targetName := o.Name
	base := o.Layer
	var baseTag string
	var baseType string

	for {
		// Iterate through base layers until we find the first one which is not types.BuiltLayer or BuildOnly

		// Need to declare ok and err  separately, if we do it in the same line as
		// assigning the new value to base, base would be a new variable only in the scope
		// of this iteration and we never meet the condition to exit the loop
		var ok bool
		var err error

		baseType = base.From.Type
		if baseType == types.ScratchLayer || baseType == types.TarLayer {
			break
		}

		baseTag, err = base.From.ParseTag()
		if err != nil {
			return err
		}

		if baseType != types.BuiltLayer {
			break
		}

		base, ok = sfm.LookupLayerDefinition(base.From.Tag)
		if !ok {
			return errors.Errorf("missing base layer: %s?", base.From.Tag)
		}

		if !base.BuildOnly {
			break
		}
	}

	if (baseType == types.ScratchLayer || baseType == types.TarLayer) && base.BuildOnly {
		// The base layers cannot be copied, so initialize an empty OCI tag.
		return umoci.NewImage(o.OCI, targetName)
	}

	if baseType != types.DockerLayer && baseType != types.OCILayer {
		return lib.ImageCopy(lib.ImageCopyOpts{
			Src:  fmt.Sprintf("oci:%s:%s", o.Config.OCIDir, baseTag),
			Dest: fmt.Sprintf("oci:%s:%s", o.Config.OCIDir, targetName),
		})
	}

	// The base image has been built separately and needs to be picked up from layer-bases
	cacheDir := path.Join(o.Config.StackerDir, "layer-bases", "oci")
	return lib.ImageCopy(lib.ImageCopyOpts{
		Src:  fmt.Sprintf("oci:%s:%s", cacheDir, baseTag),
		Dest: fmt.Sprintf("oci:%s:%s", o.Config.OCIDir, targetName),
	})
}
