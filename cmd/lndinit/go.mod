module github.com/lightningnetwork/lnd/cmd/lndinit

require (
	github.com/btcsuite/btcwallet/walletdb v1.3.6-0.20210803004036-eebed51155ec
	github.com/jessevdk/go-flags v1.4.0
	github.com/kkdai/bstream v1.0.0 // indirect
	github.com/lightningnetwork/lnd v0.13.0-beta
	github.com/lightningnetwork/lnd/kvdb v1.0.3
	github.com/onsi/ginkgo v1.11.0 // indirect
	github.com/onsi/gomega v1.7.0 // indirect
	go.etcd.io/etcd/client/v3 v3.5.0
)

replace github.com/lightningnetwork/lnd => ../../

replace github.com/lightningnetwork/lnd/kvdb => ../../kvdb

// If you change this please also update .github/pull_request_template.md,
// docs/INSTALL.md and go.mod.
go 1.15
