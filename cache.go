package stacker

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"

	"github.com/mitchellh/hashstructure"
	"github.com/openSUSE/umoci"
	"github.com/opencontainers/go-digest"
	ispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/vbatts/go-mtree"
)

const currentCacheVersion = 4

type ImportType int

const (
	ImportFile ImportType = iota
	ImportDir  ImportType = iota
)

func (it ImportType) IsDir() bool {
	return ImportDir == it
}

type ImportHash struct {
	// Unfortuantely, mtree doesn't work if you just pass it a single file,
	// so we use the sha256sum of the file, or the mtree encoding if it's a
	// directory. This indicates which.
	Type ImportType
	Hash string
}

type CacheEntry struct {
	// The manifest that this corresponds to.
	Blob ispec.Descriptor

	// A map of the import url to the base64 encoded result of mtree walk
	// or sha256 sum of a file, depending on what Type is.
	Imports map[string]ImportHash

	// The name of this layer as it was built. Useful for the BuildOnly
	// case to make sure it still exists, and for printing error messages.
	Name string

	// The layer to cache
	Layer *Layer

	// If the layer is of type "built", this is a hash of the base layer's
	// CacheEntry, which contains a hash of its imports. If there is a
	// mismatch with the current base layer's CacheEntry, the layer should
	// be rebuilt.
	Base string
}

type BuildCache struct {
	path       string
	importsDir string
	sf         *Stackerfile
	Cache      map[string]CacheEntry `json:"cache"`
	Version    int                   `json:"version"`
}

func OpenCache(config StackerConfig, oci *umoci.Layout, sf *Stackerfile) (*BuildCache, error) {
	p := path.Join(config.StackerDir, "build.cache")
	f, err := os.Open(p)
	cache := &BuildCache{
		path:       p,
		importsDir: path.Join(config.StackerDir, "imports"),
		sf:         sf,
	}

	if err != nil {
		if os.IsNotExist(err) {
			cache.Cache = map[string]CacheEntry{}
			cache.Version = currentCacheVersion
			return cache, nil
		}
		return nil, err
	}

	content, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(content, cache); err != nil {
		return nil, err
	}

	if cache.Version != currentCacheVersion {
		fmt.Println("old cache version found, clearing cache and rebuilding from scratch...")
		os.Remove(p)
		cache.Cache = map[string]CacheEntry{}
		cache.Version = currentCacheVersion
		return cache, nil
	}

	pruned := false
	for hash, ent := range cache.Cache {
		if ent.Layer.BuildOnly {
			// If this is a build only layer, we just rely on the
			// fact that it's in the rootfs dir (and hope that
			// nobody has touched it). So, let's stat its dir and
			// keep going.
			_, err = os.Stat(path.Join(config.RootFSDir, ent.Name))
		} else {
			_, err = oci.LookupManifestByDescriptor(ent.Blob)
		}

		if err != nil {
			fmt.Printf("couldn't find %s, pruning it from the cache", ent.Name)
			delete(cache.Cache, hash)
			pruned = true
		}
	}

	if pruned {
		err := cache.persist()
		if err != nil {
			return nil, err
		}
	}

	return cache, nil
}

/* Explicitly don't use mtime */
var mtreeKeywords = []mtree.Keyword{"type", "link", "uid", "gid", "xattr", "mode", "sha256digest"}

func walkImport(path string) (*mtree.DirectoryHierarchy, error) {
	return mtree.Walk(path, nil, mtreeKeywords, nil)
}

func hashFile(path string) (string, error) {
	h := sha256.New()
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}

	_, err = io.Copy(h, f)
	f.Close()
	if err != nil {
		return "", err
	}

	d := digest.NewDigest("sha256", h)
	return d.String(), nil
}

func (c *BuildCache) Lookup(name string) (*CacheEntry, bool) {
	l, ok := c.sf.Get(name)
	if !ok {
		return nil, false
	}

	result, ok := c.Cache[name]
	if !ok {
		return nil, false
	}

	baseHash, err := c.getBaseHash(name)
	if err != nil {
		return nil, false
	}

	if baseHash != result.Base {
		return nil, false
	}

	imports, err := l.ParseImport()
	if err != nil {
		return nil, false
	}

	for _, imp := range imports {
		fname := path.Base(imp)
		cachedImport, ok := result.Imports[fname]
		if !ok {
			return nil, false
		}

		diskPath := path.Join(c.importsDir, name, fname)
		st, err := os.Stat(diskPath)
		if err != nil {
			return nil, false
		}

		if cachedImport.Type.IsDir() != st.IsDir() {
			return nil, false
		}

		if st.IsDir() {
			rawCachedImport, err := base64.StdEncoding.DecodeString(cachedImport.Hash)
			if err != nil {
				return nil, false
			}

			cachedDH, err := mtree.ParseSpec(bytes.NewBuffer(rawCachedImport))
			if err != nil {
				return nil, false
			}

			dh, err := walkImport(diskPath)
			if err != nil {
				return nil, false
			}

			diff, err := mtree.Compare(cachedDH, dh, mtreeKeywords)
			if err != nil {
				return nil, false
			}

			if len(diff) > 0 {
				return nil, false
			}
		} else {
			h, err := hashFile(diskPath)
			if err != nil {
				return nil, false
			}

			if h != cachedImport.Hash {
				return nil, false
			}
		}
	}

	return &result, true
}

func getEncodedMtree(path string) (string, error) {
	dh, err := walkImport(path)
	if err != nil {
		return "", err
	}

	buf := &bytes.Buffer{}
	_, err = dh.WriteTo(buf)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func (c *BuildCache) getBaseHash(name string) (string, error) {
	l, ok := c.sf.Get(name)
	if !ok {
		return "", fmt.Errorf("%s missing from stackerfile?", name)
	}

	if l.From.Type != BuiltType {
		return "", nil
	}

	baseEnt, ok := c.Lookup(l.From.Tag)
	if !ok {
		return "", fmt.Errorf("couldn't find a cache of base layer")
	}

	baseHash, err := hashstructure.Hash(baseEnt, nil)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%d", baseHash), nil
}

func (c *BuildCache) Put(name string, blob ispec.Descriptor) error {
	l, ok := c.sf.Get(name)
	if !ok {
		return fmt.Errorf("%s missing from stackerfile?", name)
	}

	baseHash, err := c.getBaseHash(name)
	if err != nil {
		return err
	}

	ent := CacheEntry{
		Blob:    blob,
		Imports: map[string]ImportHash{},
		Name:    name,
		Layer:   l,
		Base:    baseHash,
	}

	imports, err := l.ParseImport()
	if err != nil {
		return err
	}

	for _, imp := range imports {
		fname := path.Base(imp)
		diskPath := path.Join(c.importsDir, name, fname)
		st, err := os.Stat(diskPath)
		if err != nil {
			return err
		}

		ih := ImportHash{}
		if st.IsDir() {
			ih.Type = ImportDir
			ih.Hash, err = getEncodedMtree(diskPath)
			if err != nil {
				return err
			}
		} else {
			ih.Type = ImportFile
			ih.Hash, err = hashFile(diskPath)
			if err != nil {
				return err
			}
		}

		ent.Imports[fname] = ih
	}

	c.Cache[name] = ent
	return c.persist()
}

func (c *BuildCache) persist() error {
	content, err := json.Marshal(c)
	if err != nil {
		return err
	}

	return ioutil.WriteFile(c.path, content, 0600)
}
