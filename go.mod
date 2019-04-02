module github.com/anuvu/stacker

go 1.12

require (
	code.cloudfoundry.org/systemcerts v0.0.0-20180917154049-ca00b2f806f2
	github.com/BurntSushi/toml v0.3.1
	github.com/Microsoft/go-winio v0.0.0-20190117211522-75bf6ca3d7cb
	github.com/anmitsu/go-shlex v0.0.0-20161002113705-648efa622239
	github.com/apex/log v1.1.0
	github.com/beorn7/perks v0.0.0-20180321164747-3a771d992973
	github.com/blang/semver v3.5.1+incompatible
	github.com/boltdb/bolt v0.0.0-20180302180052-fd01fc79c553
	github.com/cheggaaa/pb v1.0.27
	github.com/containerd/continuity v0.0.0-20181203112020-004b46473808
	github.com/containers/image v0.0.0-20190208010805-4629bcc4825f
	github.com/containers/storage v0.0.0-20190207215558-06b6c2e4cf25
	github.com/cyphar/filepath-securejoin v0.2.2
	github.com/docker/distribution v0.0.0-20190205005809-0d3efadf0154
	github.com/docker/docker v0.0.0-20190207111444-e6fe7f8f2936
	github.com/docker/docker-credential-helpers v0.0.0-20180925085122-123ba1b7cd64
	github.com/docker/go-connections v0.0.0-20180821093606-97c2040d34df
	github.com/docker/go-metrics v0.0.0-20181218153428-b84716841b82
	github.com/docker/go-units v0.3.3
	github.com/docker/libtrust v0.0.0-20160708172513-aabc10ec26b7
	github.com/dustin/go-humanize v1.0.0
	github.com/flosch/pongo2 v0.0.0-20181225140029-79872a7b2769
	github.com/freddierice/go-losetup v0.0.0-20170407175016-fc9adea44124
	github.com/ghodss/yaml v0.0.0-20190206175653-d4115522f0fe
	github.com/golang/protobuf v1.3.1
	github.com/gorilla/mux v1.7.0
	github.com/gorilla/websocket v0.0.0-20190205004414-7c8e298727d1
	github.com/hashicorp/errwrap v1.0.0
	github.com/hashicorp/go-multierror v1.0.0
	github.com/juju/errors v0.0.0-20190207033735-e65537c515d7
	github.com/klauspost/compress v1.4.1
	github.com/klauspost/cpuid v1.2.0
	github.com/klauspost/pgzip v1.2.1
	github.com/konsorten/go-windows-terminal-sequences v1.0.2
	github.com/lxc/lxd v0.0.0-20190208124523-fe0844d45b32
	github.com/mattn/go-colorable v0.1.1 // indirect
	github.com/mattn/go-isatty v0.0.7 // indirect
	github.com/mattn/go-runewidth v0.0.0-20181218000649-703b5e6b11ae
	github.com/matttproud/golang_protobuf_extensions v1.0.1
	github.com/mitchellh/hashstructure v1.0.0
	github.com/mohae/deepcopy v0.0.0-20170929034955-c48cc78d4826 // indirect
	github.com/mtrmac/gpgme v0.0.0-20170102180018-b2432428689c
	github.com/openSUSE/umoci v0.4.4
	github.com/opencontainers/go-digest v1.0.0-rc1
	github.com/opencontainers/image-spec v1.0.1
	github.com/opencontainers/runc v0.0.0-20190208075259-dd023c457d84
	github.com/opencontainers/runtime-spec v1.0.1
	github.com/opencontainers/runtime-tools v0.7.0
	github.com/opencontainers/selinux v1.0.0 // indirect
	github.com/pkg/errors v0.8.1
	github.com/prometheus/client_golang v0.9.1
	github.com/prometheus/client_model v0.0.0-20190129233127-fd36f4220a90
	github.com/prometheus/common v0.2.0
	github.com/prometheus/procfs v0.0.0-20190208162519-de1b801bf34b
	github.com/rootless-containers/proto v0.1.0
	github.com/sergi/go-diff v0.0.0-20180205163309-da645544ed44
	github.com/sirupsen/logrus v1.4.0
	github.com/syndtr/gocapability v0.0.0-20180916011248-d98352740cb2
	github.com/udhos/equalfile v0.3.0
	github.com/ulikunitz/xz v0.5.5
	github.com/urfave/cli v1.20.0
	github.com/vbatts/go-mtree v0.4.4
	github.com/xeipuuv/gojsonpointer v0.0.0-20180127040702-4e3ac2762d5f
	github.com/xeipuuv/gojsonreference v0.0.0-20180127040603-bd5ef7bd5415
	github.com/xeipuuv/gojsonschema v1.1.0
	golang.org/x/crypto v0.0.0-20190325154230-a5d413f7728c
	golang.org/x/net v0.0.0-20190327091125-710a502c58a2
	golang.org/x/sync v0.0.0-20181221193216-37e7f081c4d4
	golang.org/x/sys v0.0.0-20190322080309-f49334f85ddc
	google.golang.org/genproto v0.0.0-20180831171423-11092d34479b // indirect
	gopkg.in/cheggaaa/pb.v1 v1.0.27
	gopkg.in/lxc/go-lxc.v2 v2.0.0-20181227225324-7c910f8a5edc
	gopkg.in/robfig/cron.v2 v2.0.0-20150107220207-be2e0b0deed5
	gopkg.in/yaml.v2 v2.2.2
)

replace github.com/vbatts/go-mtree v0.4.4 => github.com/vbatts/go-mtree v0.4.5-0.20190122034725-8b6de6073c1a

replace github.com/openSUSE/umoci v0.4.4 => github.com/tych0/umoci v0.1.1-0.20190401143912-9989a6c702b5
