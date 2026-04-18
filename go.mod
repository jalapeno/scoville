module github.com/jalapeno/scoville

go 1.26.2

require (
	github.com/nats-io/nats.go v1.49.0
	github.com/sbezverk/gobmp v0.0.0
)

require (
	github.com/golang/glog v1.2.5 // indirect
	github.com/klauspost/compress v1.18.2 // indirect
	github.com/nats-io/nkeys v0.4.12 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/sbezverk/tools v0.0.0-20230714051746-80037ac202cf // indirect
	golang.org/x/crypto v0.46.0 // indirect
	golang.org/x/exp v0.0.0-20230713183714-613f0c0eb8a1 // indirect
	golang.org/x/sys v0.39.0 // indirect
)

replace github.com/sbezverk/gobmp => ../gobmp
