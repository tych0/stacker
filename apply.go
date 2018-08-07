package stacker

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/klauspost/pgzip"
	"github.com/openSUSE/umoci"
	"github.com/openSUSE/umoci/oci/layer"
	ispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/sergi/go-diff/diffmatchpatch"
	"golang.org/x/sys/unix"
)

type Apply struct {
	layers             []ispec.Descriptor
	baseLayers         []ispec.Descriptor
	opts               BaseLayerOpts
	storage            Storage
	considerTimestamps bool

	// state for use during/after apply
	needsLdCacheRegen bool
}

func NewApply(sf *Stackerfile, opts BaseLayerOpts, storage Storage, considerTimestamps bool) (*Apply, error) {
	a := &Apply{layers: []ispec.Descriptor{}, opts: opts, storage: storage}

	var source *umoci.Layout

	if opts.Layer.From.Type == DockerType || opts.Layer.From.Type == OCIType {
		var err error
		source, err = umoci.OpenLayout(path.Join(a.opts.Config.StackerDir, "layer-bases", "oci"))
		if err != nil {
			return nil, err
		}
		defer source.Close()
	} else if opts.Layer.From.Type == BuiltType {
		base, ok := sf.Get(opts.Layer.From.Tag)
		if !ok {
			return nil, fmt.Errorf("missing base layer?")
		}

		if base.BuildOnly {
			// XXX: this isn't actually that hard to support if we
			// need to, but I suspect we don't really. The problem
			// is that no OCI layers are generated for build-only
			// layers by design, so we can't compare which layers
			// are already used. We're smart enough to handle this
			// well, but it'll take a _lot_ longer, since we
			// re-extract everything the build-only layer is based
			// on. Anyway, let's warn people.
			if len(opts.Layer.Apply) > 0 {
				fmt.Println("WARNING: build-only base layers with apply statements may be wonky")
			}
		} else {
			source = opts.OCI
		}
	}

	if source != nil {
		tag, err := opts.Layer.From.ParseTag()
		if err != nil {
			return nil, err
		}

		manifest, err := source.LookupManifest(tag)
		if err != nil {
			return nil, err
		}

		for _, l := range manifest.Layers {
			a.layers = append(a.layers, l)
		}

		a.baseLayers = make([]ispec.Descriptor, len(a.layers))
		copy(a.baseLayers, a.layers)
	}

	return a, nil
}

func (a *Apply) DoApply() error {
	if len(a.opts.Layer.Apply) == 0 {
		return nil
	}

	err := a.storage.Snapshot(a.opts.Target, "stacker-apply-base")
	if err != nil {
		return err
	}
	defer a.storage.Delete("stacker-apply-base")

	for _, image := range a.opts.Layer.Apply {
		fmt.Println("merging in layers from", image)
		err = a.applyImage(image)
		if err != nil {
			return err
		}
	}

	if a.needsLdCacheRegen {
		err = Run(a.opts.Config, a.opts.Name, "ldconfig", a.opts.Layer, "", nil)
		if err != nil {
			return errors.Wrap(err, "error regenrating ldconfig cache")
		}
	}

	return nil
}

func (a *Apply) applyImage(layer string) error {
	err := runSkopeo(layer, a.opts, false)
	if err != nil {
		return err
	}

	oci, err := umoci.OpenLayout(path.Join(a.opts.Config.StackerDir, "layer-bases", "oci"))
	if err != nil {
		return err
	}
	defer oci.Close()

	tag, err := tagFromSkopeoUrl(layer)
	if err != nil {
		return err
	}

	manifest, err := oci.LookupManifest(tag)
	if err != nil {
		return err
	}

	for i, l := range manifest.Layers {
		// did we already extract this layer in this image?
		found := false
		for _, l2 := range a.layers {
			if l2.Digest == l.Digest {
				found = true
				break
			}
		}

		if found {
			continue
		}

		if i == 0 {
			color.Red(fmt.Sprintf("WARNING: %s does not share any layers with the base", tag))
		}

		fmt.Println("applying layer", l.Digest)

		// apply the layer. TODO: we could be smart about this if the
		// layer is strictly additive or doesn't otherwise require
		// merging, we could realize that and add it directly to the
		// OCI output, so that it is kept as its own layer.
		err = a.applyLayer(oci, l, path.Join(a.opts.Config.RootFSDir, a.opts.Target, "rootfs"))
		if err != nil {
			fmt.Println("error from applyLayer")
			return err
		}

		a.layers = append(a.layers, l)
	}

	fmt.Println("done applyImage")

	return nil
}

func (a *Apply) applyLayer(oci *umoci.Layout, desc ispec.Descriptor, target string) error {
	fmt.Println("applyLayer")
	blob, err := oci.LookupBlob(desc)
	if err != nil {
		return errors.Wrapf(err, "couldn't find blob %s", desc.Digest)
	}
	defer blob.Close()

	var reader io.ReadCloser
	switch blob.MediaType {
	case ispec.MediaTypeImageLayer:
		reader = blob.Data.(io.ReadCloser)
		// closed by blob.Close()
	case ispec.MediaTypeImageLayerGzip:
		reader, err = pgzip.NewReader(blob.Data.(io.ReadCloser))
		if err != nil {
			return err
		}
		defer reader.Close()
	default:
		return fmt.Errorf("unknown layer type %s", blob.MediaType)
	}

	didMerge := false
	tr := tar.NewReader(reader)
	te := layer.NewTarExtractor(layer.MapOptions{})
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}

		if err != nil {
			return errors.Wrapf(err, "apply layer")
		}

		// A slightly special case: we skip ., since the root directory
		// of the filesystem can be mounted with any number of
		// permissions which may not match.
		if hdr.Name == "." {
			continue
		}

		fmt.Printf("inserting one file %s...", hdr.Name)
		merged, err := a.insertOneFile(hdr, target, te, tr)
		if err != nil {
			return err
		}
		fmt.Printf("done\n")

		didMerge = didMerge || merged
	}

	if !didMerge {
		// TODO: here we can be very smart: we didn't need to merge
		// anything, so all the files were either new, or their
		// contents exactly matched the layers below. So we can just
		// inject the source layer above for this into the image,
		// instead of piling it on and generating a giant layer with
		// all of these merged files combined.
		//
		// Indeed, even if we did a merge, we could still inject this
		// old layer, since our rootfs reflects the merge when it is
		// repacked by umoci it will generate a new layer with the
		// right file content. Of course, this means the mostly same
		// file will occur multiple times in the archive, but since we
		// only do simple merging of text files, the files are
		// presumably small, and this is very cheap.
		//
		// But anyway, for now we just generate one giant blob because
		// it's less code.
	}

	return nil
}

func (a *Apply) insertOneFile(hdr *tar.Header, target string, te *layer.TarExtractor, tr io.Reader) (bool, error) {
	fi, err := os.Lstat(path.Join(target, hdr.Name))
	if os.IsNotExist(err) {
		// if it didn't already exist, that's fine, just
		// process it normally
		return false, errors.Wrapf(te.UnpackEntry(target, hdr, tr), "unpacking %s", hdr.Name)
	} else if err != nil {
		return false, errors.Wrapf(err, "stat %s", path.Join(target, hdr.Name))
	}

	if fi.Mode() != hdr.FileInfo().Mode() {
		return false, fmt.Errorf("apply can't merge files of different types: %s", hdr.Name)
	}

	sysStat := fi.Sys().(*syscall.Stat_t)

	// For everything that's not a file, we want to be sure their times are
	// identical if the user has asked for it. For files, we allow some
	// slack in case two different layers edit the file, their mtimes will
	// be different. The merging of the result is handled below.
	if a.considerTimestamps && hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
		// zero is allowed, since umoci just picks time.Now(), they
		// probably won't match.
		if fi.ModTime() != hdr.ModTime && !hdr.ModTime.IsZero() {

			// liblxc impolitely binds its own init into /tmp/.lxc-init,
			// which changes the mtime on /tmp
			if hdr.Name == "tmp/" {
				return false, nil
			}

			// we bind the host's /etc/resolv.conf to inside the container
			if hdr.Name == "etc/" {
				return false, nil
			}

			return false, fmt.Errorf("two different mod times on %s %v %v", hdr.Name, fi.ModTime(), hdr.ModTime)
		}

		// explicitly don't consider access time
		cSec, cNsec := sysStat.Ctim.Unix()
		ctime := time.Unix(cSec, cNsec)
		if ctime != hdr.ChangeTime && !hdr.ChangeTime.IsZero() {
			return false, fmt.Errorf("changed times differ on %s", hdr.Name)
		}
	}

	if sysStat.Uid != uint32(hdr.Uid) {
		return false, fmt.Errorf("two different uids on %s: %v %v", hdr.Name, sysStat.Uid, hdr.Uid)
	}

	if sysStat.Gid != uint32(hdr.Gid) {
		return false, fmt.Errorf("two different gids on %s: %v %v", hdr.Name, sysStat.Gid, hdr.Gid)
	}

	sz, err := unix.Llistxattr(path.Join(target, hdr.Name), nil)
	if err == nil {
		xattrBuf := make([]byte, sz)
		_, err = unix.Llistxattr(path.Join(target, hdr.Name), xattrBuf)
		if err != nil {
			return false, errors.Wrap(err, "error listing xattrs")
		}

		start := 0
		xattrs := []string{}
		for i, c := range xattrBuf {
			if c == 0 {
				xattrs = append(xattrs, string(xattrBuf[start:i]))
				start = i + 1
			}
		}

		if len(xattrs) != len(hdr.Xattrs) {
			return false, fmt.Errorf("different xattrs for %s: %v %v", hdr.Name, xattrs, hdr.Xattrs)
		}

		for k, v := range hdr.Xattrs {
			found := false
			for _, xattr := range xattrs {
				if fmt.Sprintf("%s=%s", k, v) == xattr {
					found = true
					break
				}
			}

			if !found {
				return false, fmt.Errorf("different xattrs for %s, missing %s=%s", hdr.Name, k, v)
			}
		}

	} else if err != syscall.ENODATA {
		return false, errors.Wrapf(err, "problem getting xattrs for %s", hdr.Name)
	}

	switch hdr.Typeflag {
	case tar.TypeDir, tar.TypeFifo:
		// no-op, already exists and matches
		return false, nil
	case tar.TypeChar, tar.TypeBlock:
		if (hdr.FileInfo().Mode()&os.ModeCharDevice != 0) != (hdr.Typeflag == tar.TypeChar) {
			if uint32(hdr.Devmajor) != unix.Major(sysStat.Dev) || uint32(hdr.Devminor) != unix.Minor(sysStat.Dev) {
				return false, fmt.Errorf("device number mismatches for %s", hdr.Name)
			}
			return false, nil
		}

		return false, fmt.Errorf("block/char mismatch: %s", hdr.Name)
	case tar.TypeLink:
		// make sure this new hard link points to the same
		// place as the existing one.
		targetFI, err := os.Lstat(path.Join(target, hdr.Linkname))
		if err != nil {
			return false, errors.Wrapf(err, "couldn't stat link %s", hdr.Linkname)
		}

		targetIno := targetFI.Sys().(*syscall.Stat_t).Ino
		curIno := fi.Sys().(*syscall.Stat_t).Ino
		if targetIno != curIno {
			return false, fmt.Errorf("hard link %s would change location", hdr.Name)
		}

		return false, nil
	case tar.TypeSymlink:
		// make sure this new symlink points to the same place
		// as the existing one.
		linkname, err := os.Readlink(path.Join(target, hdr.Name))
		if err != nil {
			return false, errors.Wrapf(err, "couldn't readlink %s", hdr.Name)
		}

		if linkname != hdr.Linkname {
			return false, fmt.Errorf("%s would change symlink from %s to %s", hdr.Name, linkname, hdr.Linkname)
		}

		return false, nil
	case tar.TypeReg, tar.TypeRegA:
		// Now the fun one. We want to do a diff of this file
		// with the existing file and try and merge them
		// somehow. If they're not mergable, then we bail. Note
		// that we don't have to check file mode, since we
		// ensured they were the same above.

		// First, write the file next to the new one. This way
		// it's on the same device, so if they're huge and
		// mergable, we don't do lots of extra IO on the final
		// rename.
		f, err := ioutil.TempFile(path.Dir(path.Join(target, hdr.Name)), "stacker-apply")
		if err != nil {
			return false, err
		}
		defer f.Close()
		defer os.Remove(f.Name())

		h := sha256.New()
		w := io.MultiWriter(f, h)

		n, err := io.Copy(w, tr)
		if err != nil {
			return false, err
		}
		if n != hdr.Size {
			return false, fmt.Errorf("%s was bad size in tar file", hdr.Name)
		}

		existing, err := os.Open(path.Join(target, hdr.Name))
		if err != nil {
			return false, errors.Wrapf(err, "couldn't open existing file %s", path.Join(target, hdr.Name))
		}
		defer existing.Close()

		if hdr.Size == fi.Size() {
			existingH := sha256.New()
			_, err = io.Copy(existingH, existing)
			if err != nil {
				return false, err
			}

			_, err = existing.Seek(0, os.SEEK_SET)
			if err != nil {
				return false, err
			}

			// The files are equal, we're ok.
			if bytes.Equal(existingH.Sum([]byte{}), h.Sum([]byte{})) {
				return false, nil
			}
		}

		// Now we know the files aren't equal. We don't want to
		// try that hard to diff things, so let's make sure we
		// only diff text files.
		buf := make([]byte, 512)
		sz, err = existing.Read(buf)
		if err != nil {
			return false, err
		}

		_, err = existing.Seek(0, os.SEEK_SET)
		if err != nil {
			return false, err
		}

		contentType := http.DetectContentType(buf[:sz])
		if !strings.HasPrefix(contentType, "text") {
			// XXX: a special case. on linux, ld.so.cache is a
			// binary file generated by ldconfig. Since it's
			// binary, we can't merge it automatically. It's
			// regenerated by package managers when new shared
			// objects are installed, which will be common with
			// library layers. Thus, two library layers will never
			// be able to be applied. Instead, let's ignore it, and
			// automatically regenerate the cache on top.
			ldCacheFiles := []string{
				"etc/ld.so.cache",
				"var/cache/ldconfig/aux-cache",
			}
			for _, cacheFile := range ldCacheFiles {
				if hdr.Name == cacheFile || hdr.Name == "/"+cacheFile {
					a.needsLdCacheRegen = true
					return false, nil
				}
			}

			return false, fmt.Errorf("existing file different, can't diff %s of type %s", hdr.Name, contentType)
		}

		// TODO: we've mutated the mtime of the directory, we should
		// probably restore it (future applies are unlikely to work if
		// we don't).
		return true, a.diffFile(hdr, f.Name())
	default:
		return false, fmt.Errorf("unknown tar typeflag for %s", hdr.Name)
	}
}

// diffFile diffs the file "temp" with the file in the original snapshot
// referred to by hdr. It returns an error if there are conflicts with a
// previous layer change, or nil if there is not. diffFile has applied the diff
// if it returns nil.
func (a *Apply) diffFile(hdr *tar.Header, temp string) error {
	// first, get the delta from the original to the layer's version
	p, err := genPatch(path.Join(a.opts.Config.RootFSDir, "stacker-apply-base/rootfs", hdr.Name), temp)
	if err != nil {
		return err
	}

	// now, apply it on top of all the other layer deltas. if it works,
	// great, if not, we bail.
	return applyPatch(path.Join(a.opts.Config.RootFSDir, a.opts.Target, "rootfs", hdr.Name), p)
}

func genPatch(p1 string, p2 string) ([]diffmatchpatch.Patch, error) {
	c1, err := ioutil.ReadFile(p1)
	if err != nil {
		// it's ok for the source file to not exist: that just means
		// that two layers added a file that didn't exist in the base
		// layer. we render it as an empty file, so both diffs will be
		// additive.
		if !os.IsNotExist(err) {
			return nil, errors.Wrapf(err, "couldn't read %s", p1)
		}
		c1 = []byte{}
	}

	c2, err := ioutil.ReadFile(p2)
	if err != nil {
		return nil, err
	}

	// This function does various things based on what type of arguments it
	// is passed. Buyer beware.
	return diffmatchpatch.New().PatchMake(string(c1), string(c2)), nil
}

func applyPatch(file string, patch []diffmatchpatch.Patch) error {
	content, err := ioutil.ReadFile(file)
	if err != nil {
		// again it's fine for it not to exist -- we just apply the
		// patche against an empty file
		if !os.IsNotExist(err) {
			return errors.Wrapf(err, "couldn't read original file %s", file)
		}
		content = []byte{}
	}

	result, applied := diffmatchpatch.New().PatchApply(patch, string(content))
	for i, app := range applied {
		if !app {
			return fmt.Errorf("couldn't merge %s, specifically hunk:\n%s", file, patch[i].String())
		}
	}

	// let's open it and truncate rather than create a new one, so we keep
	// mode/xattrs, etc.
	f, err := os.OpenFile(file, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return errors.Wrapf(err, "couldn't create patched file %s", file)
	}
	defer f.Close()

	_, err = f.WriteString(result)
	if err != nil {
		return err
	}

	return nil
}
