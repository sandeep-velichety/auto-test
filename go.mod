module github.com/testplane/testplane

go 1.24

// The replace directives below point modules served from vanity hosts
// (golang.org/x, google.golang.org, go.mongodb.org, gopkg.in) at their
// official GitHub sources. They were needed to build in a network that only
// reaches GitHub. They are harmless elsewhere; to drop them, delete every
// replace line and run `go mod tidy`.
replace (
	go.mongodb.org/mongo-driver/v2 => github.com/mongodb/mongo-go-driver/v2 v2.2.2
	golang.org/x/crypto => github.com/golang/crypto v0.36.0
	golang.org/x/net => github.com/golang/net v0.38.0
	golang.org/x/sync => github.com/golang/sync v0.12.0
	golang.org/x/sys => github.com/golang/sys v0.31.0
	golang.org/x/term => github.com/golang/term v0.30.0
	golang.org/x/text => github.com/golang/text v0.23.0
	google.golang.org/grpc => github.com/grpc/grpc-go v1.72.2
	google.golang.org/protobuf => github.com/protocolbuffers/protobuf-go v1.36.6
)

require (
	github.com/fullstorydev/grpcurl v1.9.2
	github.com/goccy/go-yaml v1.17.1
	github.com/jhump/protoreflect v1.17.0
	github.com/lib/pq v1.10.9
	github.com/segmentio/kafka-go v0.4.47
	github.com/tidwall/gjson v1.18.0
	go.mongodb.org/mongo-driver/v2 v2.2.2
	google.golang.org/grpc v1.72.2
)

require (
	github.com/bufbuild/protocompile v0.14.1 // indirect
	github.com/cncf/xds/go v0.0.0-20250121191232-2f005788dc42 // indirect
	github.com/envoyproxy/go-control-plane/envoy v1.32.4 // indirect
	github.com/envoyproxy/protoc-gen-validate v1.2.1 // indirect
	github.com/go-jose/go-jose/v4 v4.0.4 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/golang/snappy v1.0.0 // indirect
	github.com/klauspost/compress v1.16.7 // indirect
	github.com/pierrec/lz4/v4 v4.1.15 // indirect
	github.com/spiffe/go-spiffe/v2 v2.5.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.0 // indirect
	github.com/xdg-go/pbkdf2 v1.0.0 // indirect
	github.com/xdg-go/scram v1.1.2 // indirect
	github.com/xdg-go/stringprep v1.0.4 // indirect
	github.com/youmark/pkcs8 v0.0.0-20240726163527-a2c0da244d78 // indirect
	github.com/zeebo/errs v1.4.0 // indirect
	golang.org/x/crypto v0.36.0 // indirect
	golang.org/x/net v0.37.0 // indirect
	golang.org/x/sync v0.12.0 // indirect
	golang.org/x/sys v0.31.0 // indirect
	golang.org/x/text v0.23.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250218202821-56aae31c358a // indirect
	google.golang.org/protobuf v1.36.5 // indirect
)

replace gopkg.in/yaml.v3 => github.com/go-yaml/yaml v3.0.1+incompatible

replace gopkg.in/yaml.v2 => github.com/go-yaml/yaml v2.4.0+incompatible

replace gopkg.in/check.v1 => github.com/go-check/check v0.0.0-20201130134442-10cb98267c6c

replace golang.org/x/tools => github.com/golang/tools v0.31.0

replace golang.org/x/mod => github.com/golang/mod v0.24.0

replace golang.org/x/telemetry => github.com/golang/telemetry v0.0.0-20240521205824-bda55230c457

replace golang.org/x/xerrors => github.com/golang/xerrors v0.0.0-20240903120638-7835f813f4da

replace golang.org/x/oauth2 => github.com/golang/oauth2 v0.28.0

replace golang.org/x/exp => github.com/golang/exp v0.0.0-20250305212735-054e65f0b394

replace golang.org/x/lint => github.com/golang/lint v0.0.0-20210508222113-6edffad5e616

replace golang.org/x/time => github.com/golang/time v0.11.0

replace google.golang.org/genproto/googleapis/rpc => github.com/googleapis/go-genproto/googleapis/rpc v0.0.0-20250218202821-56aae31c358a

replace google.golang.org/genproto/googleapis/api => github.com/googleapis/go-genproto/googleapis/api v0.0.0-20250218202821-56aae31c358a

replace google.golang.org/genproto => github.com/googleapis/go-genproto v0.0.0-20250218202821-56aae31c358a
