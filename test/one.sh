#!/bin/bash

set -e

export STACKER_KEEP=1

if [ -z "$GOPATH" ]; then
    echo "no GOPATH, try sudo -E ./main.sh"
    exit 1
fi

if [ "$(id -u)" != "0" ]; then
    echo "you should be root to run this suite"
    exit 1
fi

PATH=$PATH:$GOPATH/bin

function sha() {
    echo $(sha256sum $1 | cut -f1 -d" ")
}

function cleanup() {
    umount roots >& /dev/null || true
    rm -rf roots oci dest >& /dev/null || true
    if [ -z "$STACKER_KEEP" ]; then
        rm -rf .stacker >& /dev/null || true
    else
        rm -rf .stacker/logs .stacker/btrfs.loop .stacker/build.cache
    fi
    echo done with testing: $RESULT
}

function on_exit() {
    set +x
    if [ "$RESULT" != "success" ]; then
        if [ -n "$STACKER_INSPECT" ]; then
            echo "waiting for inspection; press enter to continue cleanup"
            read -r foo
        fi
        RESULT=failure
    fi
    cleanup
}
trap on_exit EXIT HUP INT TERM

# Ok, now let's try a rootless stacker.
mkdir -p .stacker
truncate -s 100G .stacker/btrfs.loop
mkfs.btrfs .stacker/btrfs.loop
mkdir -p roots
mount -o loop .stacker/btrfs.loop roots
chown -R $SUDO_USER:$SUDO_USER roots
sudo -u $SUDO_USER $GOPATH/bin/stacker build -f ./import-docker.yaml
umoci unpack --image oci:layer1 dest

false

RESULT=success
