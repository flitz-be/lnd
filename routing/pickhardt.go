package routing

import (
	"fmt"
	"time"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcutil"
	"github.com/lightningnetwork/lnd/channeldb"
	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/lnwire"
	"github.com/lightningnetwork/lnd/routing/route"
)

func ImportGraphData(graph *lnrpc.ChannelGraph, graphDB *channeldb.ChannelGraph,
	chainGenesisHash chainhash.Hash) error {

	var err error
	for _, rpcNode := range graph.Nodes {
		node := &channeldb.LightningNode{
			HaveNodeAnnouncement: true,
			LastUpdate: time.Unix(
				int64(rpcNode.LastUpdate), 0,
			),
			Alias: rpcNode.Alias,
		}

		node.PubKeyBytes, err = route.NewVertexFromStr(rpcNode.PubKey)
		if err != nil {
			return err
		}

		featureBits := make([]lnwire.FeatureBit, 0, len(rpcNode.Features))
		featureNames := make(map[lnwire.FeatureBit]string)

		for featureBit, feature := range rpcNode.Features {
			featureBits = append(
				featureBits, lnwire.FeatureBit(featureBit),
			)

			featureNames[lnwire.FeatureBit(featureBit)] = feature.Name
		}

		featureVector := lnwire.NewRawFeatureVector(featureBits...)
		node.Features = lnwire.NewFeatureVector(
			featureVector, featureNames,
		)

		node.Color, err = lnrpc.ParseHexColor(rpcNode.Color)
		if err != nil {
			return err
		}

		if err := graphDB.AddLightningNode(node); err != nil {
			return fmt.Errorf("unable to add node %v: %v",
				rpcNode.PubKey, err)
		}

		log.Debugf("Imported node: %v", rpcNode.PubKey)
	}

	for _, rpcEdge := range graph.Edges {
		rpcEdge := rpcEdge

		edge := &channeldb.ChannelEdgeInfo{
			ChannelID: rpcEdge.ChannelId,
			ChainHash: chainGenesisHash,
			Capacity:  btcutil.Amount(rpcEdge.Capacity),
		}

		edge.NodeKey1Bytes, err = route.NewVertexFromStr(rpcEdge.Node1Pub)
		if err != nil {
			return err
		}

		edge.NodeKey2Bytes, err = route.NewVertexFromStr(rpcEdge.Node2Pub)
		if err != nil {
			return err
		}

		channelPoint, err := lnrpc.NewWireOutPoint(rpcEdge.ChanPoint)
		if err != nil {
			return err
		}
		edge.ChannelPoint = *channelPoint

		if err := graphDB.AddChannelEdge(edge); err != nil {
			return fmt.Errorf("unable to add edge %v: %v",
				rpcEdge.ChanPoint, err)
		}

		makePolicy := func(
			rpcPolicy *lnrpc.RoutingPolicy) *channeldb.ChannelEdgePolicy {

			policy := &channeldb.ChannelEdgePolicy{
				ChannelID: rpcEdge.ChannelId,
				LastUpdate: time.Unix(
					int64(rpcPolicy.LastUpdate), 0,
				),
				TimeLockDelta: uint16(
					rpcPolicy.TimeLockDelta,
				),
				MinHTLC: lnwire.MilliSatoshi(
					rpcPolicy.MinHtlc,
				),
				FeeBaseMSat: lnwire.MilliSatoshi(
					rpcPolicy.FeeBaseMsat,
				),
				FeeProportionalMillionths: lnwire.MilliSatoshi(
					rpcPolicy.FeeRateMilliMsat,
				),
			}
			if rpcPolicy.MaxHtlcMsat > 0 {
				policy.MaxHTLC = lnwire.MilliSatoshi(
					rpcPolicy.MaxHtlcMsat,
				)
				policy.MessageFlags |= lnwire.ChanUpdateOptionMaxHtlc
			}

			return policy
		}

		if rpcEdge.Node1Policy != nil {
			policy := makePolicy(rpcEdge.Node1Policy)
			policy.ChannelFlags = 0
			if err := graphDB.UpdateEdgePolicy(policy); err != nil {
				return fmt.Errorf(
					"unable to update policy: %v", err)
			}
		}

		if rpcEdge.Node2Policy != nil {
			policy := makePolicy(rpcEdge.Node2Policy)
			policy.ChannelFlags = 1
			if err := graphDB.UpdateEdgePolicy(policy); err != nil {
				return fmt.Errorf(
					"unable to update policy: %v", err)
			}
		}

		log.Debugf("Added edge: %v", rpcEdge.ChannelId)
	}

	return nil
}
