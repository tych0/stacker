#!/bin/bash -e

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
    set +x
    if [ "$RESULT" != "success" ]; then
        if [ -n "$STACKER_INSPECT" ]; then
            echo "waiting for inspection; press enter to continue cleanup"
            read -r foo
        fi
        RESULT=failure
    fi
    umount roots >& /dev/null || true
    rm -rf roots oci >& /dev/null || true
    if [ -z "$STACKER_KEEP" ]; then
        rm -rf .stacker >& /dev/null || true
    else
        rm -rf .stacker/logs .stacker/btrfs.loop .stacker/build.cache
    fi
    echo done with testing: $RESULT
}
trap cleanup EXIT HUP INT TERM

# clean up old logs if they exist
if [ -n "$STACKER_KEEP" ]; then
    rm -rf .stacker/logs .stacker/btrfs.loop
fi

set -x

stacker build --leave-unladen -f ./basic.yaml

# did we really download the image?
[ -f .stacker/layer-bases/centos.tar.xz ]

# did we do a copy correctly?
[ "$(sha .stacker/imports/centos/basic.yaml)" == "$(sha ./basic.yaml)" ]

function check_image() {
    # did run actually copy the favicon to the right place?
    [ "$(sha .stacker/imports/centos/favicon.ico)" == "$(sha roots/centos/favicon.ico)" ]
}

check_image

umount roots
rm -rf .stacker/btrfs.loop
stacker unlade

check_image

# check OCI image generation
manifest=$(cat oci/index.json | jq -r .manifests[0].digest | cut -f2 -d:)
layer=$(cat oci/blobs/sha256/$manifest | jq -r .layers[0].digest)
config=$(cat oci/blobs/sha256/$manifest | jq -r .config.digest | cut -f2 -d:)
diffid=$(cat oci/blobs/sha256/$config | jq -r .rootfs.diff_ids[0])
[ "$layer" = "$diffid" ]
[ "$(cat oci/blobs/sha256/$config | jq -r '.config.Entrypoint | join(" ")')" = "echo hello world" ]

# ok, now let's do the build again. it should all be the same, since it's all cached
stacker build -f ./basic.yaml
manifest2=$(cat oci/index.json | jq -r .manifests[0].digest | cut -f2 -d:)
[ "$manifest" = "$manifest2" ]
layer2=$(cat oci/blobs/sha256/$manifest | jq -r .layers[0].digest)
[ "$layer" = "$layer2" ]

RESULT=success
