load helpers

function setup() {
    stacker_setup
    stacker unpriv-setup
}

function teardown() {
    cleanup
}

@test "unprivileged stacker" {
    [ -z "$TRAVIS" ] || skip "skipping unprivileged test in travis"

    cat > stacker.yaml <<EOF
centos:
    from:
        type: docker
        url: docker://centos:latest
    import:
        - https://www.cisco.com/favicon.ico
    run: |
        cp /stacker/favicon.ico /favicon.ico
layer1:
    from:
        type: built
        tag: centos
    run:
        - rm /favicon.ico
EOF
    chown -R $SUDO_USER:$SUDO_USER .
    sudo -u $SUDO_USER "${ROOT_DIR}/stacker" build
    umoci unpack --image oci:layer1 dest

    [ "$(sha .stacker/imports/centos/favicon.ico)" == "$(sha roots/centos/rootfs/favicon.ico)" ]
    [ ! -f dest/rootfs/favicon.ico ]
}
