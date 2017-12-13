package stacker

import (
	"os"
	"io/ioutil"
	"path"
	"path/filepath"
)

type DiffFunc func(path1 string, info1 os.FileInfo, path2 string, info2 os.FileInfo) error

func DirectoryDiff(path1 string, path2 string, diff DiffFunc) error {
	dir1, err := ioutil.ReadDir(path1)
	if err != nil {
		return err
	}

	dir2, err := ioutil.ReadDir(path2)
	if err != nil {
		return err
	}

	for _, e1 := range dir1 {
		found := true

		for _, e2 := range dir2 {
			if e1.Name() == e2.Name() {
				if !os.SameFile(e1, e2) {
					if err := diff(path.Join(path1, e1.Name()), e1, path.Join(path2, e2.Name()), e2); err != nil {
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
