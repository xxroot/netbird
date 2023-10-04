module github.com/netbirdio/netbird

go 1.20

require (
	github.com/cenkalti/backoff/v4 v4.1.3
	github.com/golang-jwt/jwt v3.2.2+incompatible
	github.com/golang/protobuf v1.5.3
	github.com/google/uuid v1.3.0
	github.com/gorilla/mux v1.8.0
	github.com/kardianos/service v1.2.1-0.20210728001519-a323c3813bc7
	github.com/onsi/ginkgo v1.16.5
	github.com/onsi/gomega v1.18.1
	github.com/pion/ice/v2 v2.3.1
	github.com/rs/cors v1.8.0
	github.com/sirupsen/logrus v1.9.0
	github.com/spf13/cobra v1.6.1
	github.com/spf13/pflag v1.0.5
	github.com/vishvananda/netlink v1.1.0
	golang.org/x/crypto v0.9.0
	golang.org/x/sys v0.8.0
	golang.zx2c4.com/wireguard v0.0.0-20230223181233-21636207a675
	golang.zx2c4.com/wireguard/wgctrl v0.0.0-20211215182854-7a385b3431de
	golang.zx2c4.com/wireguard/windows v0.5.3
	google.golang.org/grpc v1.55.0
	google.golang.org/protobuf v1.30.0
	gopkg.in/natefinch/lumberjack.v2 v2.0.0
)

require (
	fyne.io/fyne/v2 v2.1.4
	github.com/TheJumpCloud/jcapi-go v3.0.0+incompatible
	github.com/c-robinson/iplib v1.0.3
	github.com/cilium/ebpf v0.10.0
	github.com/coreos/go-iptables v0.7.0
	github.com/creack/pty v1.1.18
	github.com/eko/gocache/v3 v3.1.1
	github.com/getlantern/systray v1.2.1
	github.com/gliderlabs/ssh v0.3.4
	github.com/godbus/dbus/v5 v5.1.0
	github.com/golang/mock v1.6.0
	github.com/google/go-cmp v0.5.9
	github.com/google/gopacket v1.1.19
	github.com/google/nftables v0.0.0-20220808154552-2eca00135732
	github.com/hashicorp/go-secure-stdlib/base62 v0.1.2
	github.com/hashicorp/go-version v1.6.0
	github.com/libp2p/go-netroute v0.2.0
	github.com/magiconair/properties v1.8.5
	github.com/mattn/go-sqlite3 v1.14.17
	github.com/mdlayher/socket v0.4.0
	github.com/miekg/dns v1.1.43
	github.com/mitchellh/hashstructure/v2 v2.0.2
	github.com/nadoo/ipset v0.5.0
	github.com/okta/okta-sdk-golang/v2 v2.18.0
	github.com/patrickmn/go-cache v2.1.0+incompatible
	github.com/pion/logging v0.2.2
	github.com/pion/stun v0.4.0
	github.com/pion/transport/v2 v2.0.2
	github.com/prometheus/client_golang v1.14.0
	github.com/rs/xid v1.3.0
	github.com/skratchdot/open-golang v0.0.0-20200116055534-eef842397966
	github.com/stretchr/testify v1.8.1
	github.com/yusufpapurcu/wmi v1.2.3
	go.opentelemetry.io/otel v1.11.1
	go.opentelemetry.io/otel/exporters/prometheus v0.33.0
	go.opentelemetry.io/otel/metric v0.33.0
	go.opentelemetry.io/otel/sdk/metric v0.33.0
	goauthentik.io/api/v3 v3.2023051.3
	golang.org/x/exp v0.0.0-20220518171630-0b5c67f07fdf
	golang.org/x/mobile v0.0.0-20190719004257-d2bd2a29d028
	golang.org/x/net v0.10.0
	golang.org/x/oauth2 v0.8.0
	golang.org/x/sync v0.2.0
	golang.org/x/term v0.8.0
	google.golang.org/api v0.126.0
	gopkg.in/yaml.v3 v3.0.1
	gorm.io/driver/sqlite v1.5.3
	gorm.io/gorm v1.25.4
)

require (
	cloud.google.com/go/compute v1.19.3 // indirect
	cloud.google.com/go/compute/metadata v0.2.3 // indirect
	github.com/BurntSushi/toml v1.2.1 // indirect
	github.com/XiaoMi/pegasus-go-client v0.0.0-20210427083443-f3b6b08bc4c2 // indirect
	github.com/anmitsu/go-shlex v0.0.0-20200514113438-38f4b401e2be // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/bradfitz/gomemcache v0.0.0-20220106215444-fb4bf637b56d // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dgraph-io/ristretto v0.1.1 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/fredbi/uri v0.0.0-20181227131451-3dcfdacbaaf3 // indirect
	github.com/fsnotify/fsnotify v1.6.0 // indirect
	github.com/getlantern/context v0.0.0-20190109183933-c447772a6520 // indirect
	github.com/getlantern/errors v0.0.0-20190325191628-abdb3e3e36f7 // indirect
	github.com/getlantern/golog v0.0.0-20190830074920-4ef2e798c2d7 // indirect
	github.com/getlantern/hex v0.0.0-20190417191902-c6586a6fe0b7 // indirect
	github.com/getlantern/hidden v0.0.0-20190325191715-f02dbb02be55 // indirect
	github.com/getlantern/ops v0.0.0-20190325191751-d70cb0d6f85f // indirect
	github.com/go-gl/gl v0.0.0-20210813123233-e4099ee2221f // indirect
	github.com/go-gl/glfw/v3.3/glfw v0.0.0-20211024062804-40e447a793be // indirect
	github.com/go-logr/logr v1.2.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-ole/go-ole v1.2.6 // indirect
	github.com/go-redis/redis/v8 v8.11.5 // indirect
	github.com/go-stack/stack v1.8.0 // indirect
	github.com/goki/freetype v0.0.0-20181231101311-fa8a33aabaff // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/google/s2a-go v0.1.4 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.2.3 // indirect
	github.com/googleapis/gax-go/v2 v2.10.0 // indirect
	github.com/hashicorp/go-uuid v1.0.2 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/josharian/native v1.0.0 // indirect
	github.com/kelseyhightower/envconfig v1.4.0 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.4 // indirect
	github.com/mdlayher/genetlink v1.1.0 // indirect
	github.com/mdlayher/netlink v1.7.1 // indirect
	github.com/nxadm/tail v1.4.8 // indirect
	github.com/oxtoacart/bpool v0.0.0-20190530202638-03653db5a59c // indirect
	github.com/pegasus-kv/thrift v0.13.0 // indirect
	github.com/pion/dtls/v2 v2.2.6 // indirect
	github.com/pion/mdns v0.0.7 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/turn/v2 v2.1.0 // indirect
	github.com/pion/udp/v2 v2.0.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/prometheus/client_model v0.3.0 // indirect
	github.com/prometheus/common v0.37.0 // indirect
	github.com/prometheus/procfs v0.8.0 // indirect
	github.com/spf13/cast v1.5.0 // indirect
	github.com/srwiley/oksvg v0.0.0-20200311192757-870daf9aa564 // indirect
	github.com/srwiley/rasterx v0.0.0-20200120212402-85cb7272f5e9 // indirect
	github.com/vishvananda/netns v0.0.0-20211101163701-50045581ed74 // indirect
	github.com/yuin/goldmark v1.4.13 // indirect
	go.opencensus.io v0.24.0 // indirect
	go.opentelemetry.io/otel/sdk v1.11.1 // indirect
	go.opentelemetry.io/otel/trace v1.11.1 // indirect
	golang.org/x/image v0.5.0 // indirect
	golang.org/x/mod v0.8.0 // indirect
	golang.org/x/text v0.9.0 // indirect
	golang.org/x/time v0.0.0-20210723032227-1f47c861a9ac // indirect
	golang.org/x/tools v0.6.0 // indirect
	golang.zx2c4.com/wintun v0.0.0-20230126152724-0fa3db229ce2 // indirect
	google.golang.org/appengine v1.6.7 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20230530153820-e85fd2cbaebc // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	gopkg.in/square/go-jose.v2 v2.6.0 // indirect
	gopkg.in/tomb.v1 v1.0.0-20141024135613-dd632973f1e7 // indirect
	gopkg.in/tomb.v2 v2.0.0-20161208151619-d5d1b5820637 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	honnef.co/go/tools v0.2.2 // indirect
	k8s.io/apimachinery v0.23.5 // indirect
)

replace github.com/kardianos/service => github.com/netbirdio/service v0.0.0-20230215170314-b923b89432b0

replace github.com/getlantern/systray => github.com/netbirdio/systray v0.0.0-20221012095658-dc8eda872c0c

replace golang.zx2c4.com/wireguard => github.com/netbirdio/wireguard-go v0.0.0-20230524172305-5a498a82b33f
