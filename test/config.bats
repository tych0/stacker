load helpers

function teardown() {
    cleanup
    umount args-roots || true
    umount config-roots || true
    rm -rf *-oci *-stacker *-roots config.yaml || true
    if [ -f "$BATS_TMPDIR/config-subs/config.yaml" ]; then
        stacker --config=${BATS_TMPDIR}/config-subs/config.yaml clean --all
    fi
    rm -rf "$BATS_TMPDIR/config-subs"
}

@test "config args work" {
    cat > stacker.yaml <<EOF
test:
    from:
        type: scratch
EOF

    stacker --oci-dir args-oci --stacker-dir args-stacker --roots-dir args-roots build --leave-unladen
    [ -d args-oci ]
    [ -d args-stacker ]
    [ -d args-roots ]
}

@test "config file works" {
    cat > stacker.yaml <<EOF
test:
    from:
        type: scratch
EOF
    cat > config.yaml <<EOF
stacker_dir: config-stacker
oci_dir: config-oci
rootfs_dir: config-roots
EOF

    stacker --config config.yaml build --leave-unladen
    ls
    [ -d config-oci ]
    [ -d config-stacker ]
    [ -d config-roots ]
}

@test "config file substitutions work" {
    # the stacker file provided runs a 'my-build' that creates /my-publish/output.tar
    # output.tar's content.txt file should have the rendered values
    # for STACKER_ROOTFS_DIR STACKER_OCI_DIR and STACKER_STACKER_DIR
    #
    # my-base then imports that output file using the expansion of STACKER_ROOTFS_DIR.
    # the test compares that the rootfs from my-base contains the expected content.txt.
    local tmpd="$BATS_TMPDIR/config-subs"
    local sdir="$tmpd/stacker.d" odir="$tmpd/oci.d" rdir="$tmpd/roots.d"
    local stacker_yaml="$tmpd/stacker.yaml" config_yaml="$tmpd/config.yaml"
    local expected="$tmpd/expected.txt"

    mkdir "$tmpd"

    printf "%s\n%s\n%s\n" \
        "found rootfs=$rdir" "found oci=$odir" "found stacker=$sdir" > "$expected"

    cat > "$stacker_yaml" <<"EOF"
my-build:
    build_only: true
    from:
        type: docker
        url: docker://busybox:latest
    run: |
        #!/bin/sh
        set -e
        set -x
        outd=/my-publish
        rm -Rf "$outd"
        mkdir -p "$outd"
        cd "$outd"
        cat > content.txt <<EOF
        found rootfs=${{STACKER_ROOTFS_DIR}}
        found oci=${{STACKER_OCI_DIR}}
        found stacker=${{STACKER_STACKER_DIR}}
        EOF
        tar -cf output.tar content.txt

my-base:
    from:
        type: tar
        url: ${{STACKER_ROOTFS_DIR}}/my-build/rootfs/my-publish/output.tar
EOF

    cat > "$config_yaml" <<EOF
stacker_dir: $sdir
oci_dir: $odir
rootfs_dir: $rdir
EOF

    stacker "--config=$config_yaml" build "--stacker-file=$stacker_yaml" --leave-unladen

    cmp_files "$expected" "$rdir/my-base/rootfs/content.txt"
}
