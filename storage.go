package stacker

import (
	"os"

	"github.com/anuvu/stacker/btrfs"
	"github.com/anuvu/stacker/overlay"
	"github.com/anuvu/stacker/types"
	"github.com/pkg/errors"
)

func NewStorage(c types.StackerConfig) (types.Storage, error) {
	if err := os.MkdirAll(c.RootFSDir, 0755); err != nil {
		return nil, err
	}

	switch c.StorageType {
	case "overlay":
		overlayOk, err := overlay.CanDoOverlay()
		if err != nil {
			return nil, err
		}

		if !overlayOk {
			return nil, errors.Errorf("can't do overlay operations but overlay backend requested")
		}
		return overlay.NewOverlay(c), nil
	case "btrfs":
		isBtrfs, err := btrfs.DetectBtrfs(c.RootFSDir)
		if err != nil {
			return nil, err
		}

		if !isBtrfs {
			return btrfs.NewLoopback(c)
		}

		return btrfs.NewExisting(c), nil
	default:
		return nil, errors.Errorf("unknown storage type %s", c.StorageType)
	}
}
