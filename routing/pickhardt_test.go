package routing

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/lightninglabs/protobuf-hex-display/jsonpb"
	"github.com/lightningnetwork/lnd/channeldb"
	"github.com/lightningnetwork/lnd/kvdb"
	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/stretchr/testify/require"
)

const (
	mainnetGraphFilePath = "testdata/mainnet_graph.json"
)

func getTestGraph(path string) (*channeldb.ChannelGraph, func(), error) {
	graphJSON, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}

	jsonGraph := &lnrpc.ChannelGraph{}
	err = jsonpb.Unmarshal(bytes.NewReader(graphJSON), jsonGraph)
	if err != nil {
		return nil, nil, fmt.Errorf("error parsing JSON: %v", err)
	}

	// Next, create a temporary graph database for usage within the test.
	graph, _, cleanUp, err := makeTestGraph(true)
	if err != nil {
		return nil, nil, err
	}

	err = ImportGraphData(
		jsonGraph, graph, *chaincfg.TestNet3Params.GenesisHash,
	)
	if err != nil {
		cleanUp()
		return nil, nil, err
	}

	return graph, cleanUp, nil
}

func TestGraph(t *testing.T) {
	start := time.Now()

	testGraph, cleanUp, err := getTestGraph(mainnetGraphFilePath)
	require.NoError(t, err)

	defer cleanUp()

	loadTime := time.Since(start)

	start = time.Now()
	numNodes := 0
	err = testGraph.ForEachNodeCacheable(
		func(tx kvdb.RTx, node channeldb.GraphCacheNode) error {
			numNodes++
			return nil
		},
	)
	require.NoError(t, err)
	countTime := time.Since(start)

	t.Logf("Graph load took %v, count took %v, got %d nodes", loadTime,
		countTime, numNodes)
}
