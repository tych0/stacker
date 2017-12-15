package stacker

import (
	"bytes"
	"io"
	"fmt"
	"archive/tar"
	"os"
	"io/ioutil"
	"path"
	"path/filepath"
)

type diffFunc func(path1 string, info1 os.FileInfo, path2 string, info2 os.FileInfo) error

func directoryDiff(path1 string, path2 string, diff diffFunc) error {
	dir1, err := ioutil.ReadDir(path1)
	if err != nil {
		return err
	}

	dir2, err := ioutil.ReadDir(path2)
	if err != nil {
		return err
	}

	for _, e1 := range dir1 {
		found := false
		p1 := path.Join(path1, e1.Name())

		for _, e2 := range dir2 {
			p2 := path.Join(path2, e2.Name())
			if e1.Name() == e2.Name() {
				if e1.IsDir() {
					if e2.IsDir() {
						if err := directoryDiff(p1, p2, diff); err != nil {
							return err
						}

						found = true
						break
					}

					return fmt.Errorf("adding new directory where file was not current supported")
				}

				if !os.SameFile(e1, e2) {
					if err := diff(p1, e1, p2, e2); err != nil {
						return err
					}
				}

				found = true
				break
			}
		}

		if !found {
			p := path.Join(path1, e1.Name())
			err := filepath.Walk(p, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}

				return diff(path, info, "", nil)
			})
			if err != nil {
				return err
			}
		}
	}

	for _, e2 := range dir2 {
		found := false

		for _, e1 := range dir1 {
			if e1.Name() == e2.Name() {
				found = true
				break
			}
		}

		if !found {
			p := path.Join(path2, e2.Name())
			err := filepath.Walk(p, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}

				return diff("", nil, path, info)
			})
			if err != nil {
				return err
			}
		}
	}

	return nil
}

type chanReader struct {
	ch chan []byte
	cur io.Reader
}

func (r *chanReader) Read(p []byte) (int, error) {
	if r.cur == nil {
		bs, ok := <-r.ch
		if !ok {
			return 0, io.EOF
		}

		r.cur = bytes.NewReader(bs)
	}

	n, err := r.cur.Read(p)
	if err == io.EOF {
		r.cur = nil
		err = nil
	}
	return n, err
}

func doTarDiff(source, target string, w *io.PipeWriter) {
	tw := tar.NewWriter(w)
	diffFunc := func(path1 string, info1 os.FileInfo, path2 string, info2 os.FileInfo) error {
		var header *tar.Header
		var content io.Reader

		// remove the file
		if path2 == "" {
			whiteout := path.Join(path.Base(path1[len(source):]), fmt.Sprintf(".wh.%s", info1.Name()))
			header = &tar.Header{
				Name: whiteout,
				Mode: 0644,
				Typeflag: tar.TypeReg,
			}
			fmt.Printf("added whiteout file %s\n", header.Name)
		} else {
			// the file added or was changed, copy the v2 version in

			// nothing needed for directories
			if info2.IsDir() {
				return nil
			}
			var link string
			if info2.Mode() & os.ModeSymlink != 0 {
				var err error
				link, err = os.Readlink(path2)
				if err != nil {
					return err
				}
			} else {
				f, err := os.Open(path2)
				if err != nil {
					return err
				}
				defer f.Close()
				content = f
			}

			var err error
			header, err = tar.FileInfoHeader(info2, link)
			if err != nil {
				return err
			}

			// fix up the path
			header.Name = path2[len(target):]
		}

		err := tw.WriteHeader(header)
		if err != nil {
			return err
		}

		if content != nil {
			_, err := io.Copy(tw, content)
			return err
		}

		return nil

	}

	if source == "" {
		err := filepath.Walk(target, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			return diffFunc("", nil, path, info)
		})
		w.CloseWithError(err)
	} else {
		w.CloseWithError(directoryDiff(source, target, diffFunc))
	}
}

func tarDiff(source string, target string) (io.ReadCloser, error) {
	r, w := io.Pipe()
	go doTarDiff(source, target, w)
	return r, nil
}
