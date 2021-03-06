load helpers

function setup() {
    stacker_setup
}

function teardown() {
    cleanup
}

@test "clean on a non-loopback btrfs works" {
    require_storage btrfs

    truncate -s 10G btrfs.loop
    mkfs.btrfs btrfs.loop
    mkdir -p parent
    mount -o loop,user_subvol_rm_allowed btrfs.loop parent
    mkdir -p parent/roots

    stacker --roots-dir=parent/roots clean --all
}

@test "clean in the face of subvolumes works" {
    require_storage btrfs

    truncate -s 10G btrfs.loop
    mkfs.btrfs btrfs.loop
    mkdir -p parent
    mount -o loop,user_subvol_rm_allowed btrfs.loop parent
    mkdir -p parent/roots

    # create some subvolumes and make them all readonly
    btrfs subvol create parent/roots/a
    btrfs property set -ts parent/roots/a ro true
    btrfs subvol create parent/roots/b
    btrfs property set -ts parent/roots/b ro true
    btrfs subvol create parent/roots/c
    btrfs property set -ts parent/roots/c ro true

    # stacker clean with a roots dir that is already on btrfs should succeed
    stacker --roots-dir=parent/roots clean --all

    [ -d parent ]
    [ ! -d parent/roots ]
}

@test "unpriv subvol clean works" {
    require_storage btrfs

    truncate -s 10G btrfs.loop
    mkfs.btrfs btrfs.loop
    mkdir -p parent
    mount -o loop,user_subvol_rm_allowed btrfs.loop parent
    mkdir -p parent/roots

    # create some subvolumes and make them all readonly
    btrfs subvol create parent/roots/a
    btrfs subvol create parent/roots/a/b
    sudo chown -R $SUDO_USER:$SUDO_USER .
    btrfs property set -ts parent/roots/a/b ro true
    btrfs property set -ts parent/roots/a ro true

    sudo -u $SUDO_USER "${ROOT_DIR}/stacker" --roots-dir=parent/roots clean
    [ ! -d parent/roots/a ]
    [ ! -d parent/roots/a/b ]
}

@test "extra dirs don't get cleaned" {
    require_storage btrfs

    truncate -s 10G btrfs.loop
    mkfs.btrfs btrfs.loop
    mkdir -p parent
    mount -o loop,user_subvol_rm_allowed btrfs.loop parent
    mkdir -p parent/roots

    btrfs subvol create parent/roots/a
    # we had a bad bug one time where we forgot to join the root path with the
    # subvolume we were deleting, so these got deleted.
    mkdir a
    stacker --roots-dir=parent/roots clean
    [ ! -d parent/roots/a ]
    [ -d a ]
}

@test "clean in loopback mode works" {
    require_storage btrfs

    cat > stacker.yaml <<EOF
test:
    from:
        type: docker
        url: docker://centos:latest
EOF
    stacker build --leave-unladen
    stacker clean --all
    [ ! -d roots ]
}
