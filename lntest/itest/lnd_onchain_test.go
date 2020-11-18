package itest

import (
	"bytes"
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"math"
	"strings"
	"time"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcutil"
	"github.com/btcsuite/btcwallet/wallet"
	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/lnrpc/walletrpc"
	"github.com/lightningnetwork/lnd/lntest"
	"github.com/lightningnetwork/lnd/lntest/wait"
	"github.com/lightningnetwork/lnd/sweep"
)

// testCPFP ensures that the daemon can bump an unconfirmed  transaction's fee
// rate by broadcasting a Child-Pays-For-Parent (CPFP) transaction.
//
// TODO(wilmer): Add RBF case once btcd supports it.
func testCPFP(net *lntest.NetworkHarness, t *harnessTest) {
	// Skip this test for neutrino, as it's not aware of mempool
	// transactions.
	if net.BackendCfg.Name() == "neutrino" {
		t.Skipf("skipping reorg test for neutrino backend")
	}

	// We'll start the test by sending Alice some coins, which she'll use to
	// send to Bob.
	ctxb := context.Background()
	ctxt, _ := context.WithTimeout(ctxb, defaultTimeout)
	err := net.SendCoins(ctxt, btcutil.SatoshiPerBitcoin, net.Alice)
	if err != nil {
		t.Fatalf("unable to send coins to alice: %v", err)
	}

	// Create an address for Bob to send the coins to.
	addrReq := &lnrpc.NewAddressRequest{
		Type: lnrpc.AddressType_WITNESS_PUBKEY_HASH,
	}
	ctxt, _ = context.WithTimeout(ctxb, defaultTimeout)
	resp, err := net.Bob.NewAddress(ctxt, addrReq)
	if err != nil {
		t.Fatalf("unable to get new address for bob: %v", err)
	}

	// Send the coins from Alice to Bob. We should expect a transaction to
	// be broadcast and seen in the mempool.
	sendReq := &lnrpc.SendCoinsRequest{
		Addr:   resp.Address,
		Amount: btcutil.SatoshiPerBitcoin,
	}
	ctxt, _ = context.WithTimeout(ctxb, defaultTimeout)
	if _, err = net.Alice.SendCoins(ctxt, sendReq); err != nil {
		t.Fatalf("unable to send coins to bob: %v", err)
	}

	txid, err := waitForTxInMempool(net.Miner.Node, minerMempoolTimeout)
	if err != nil {
		t.Fatalf("expected one mempool transaction: %v", err)
	}

	// We'll then extract the raw transaction from the mempool in order to
	// determine the index of Bob's output.
	tx, err := net.Miner.Node.GetRawTransaction(txid)
	if err != nil {
		t.Fatalf("unable to extract raw transaction from mempool: %v",
			err)
	}
	bobOutputIdx := -1
	for i, txOut := range tx.MsgTx().TxOut {
		_, addrs, _, err := txscript.ExtractPkScriptAddrs(
			txOut.PkScript, net.Miner.ActiveNet,
		)
		if err != nil {
			t.Fatalf("unable to extract address from pkScript=%x: "+
				"%v", txOut.PkScript, err)
		}
		if addrs[0].String() == resp.Address {
			bobOutputIdx = i
		}
	}
	if bobOutputIdx == -1 {
		t.Fatalf("bob's output was not found within the transaction")
	}

	// Wait until bob has seen the tx and considers it as owned.
	op := &lnrpc.OutPoint{
		TxidBytes:   txid[:],
		OutputIndex: uint32(bobOutputIdx),
	}
	assertWalletUnspent(t, net.Bob, op)

	// We'll attempt to bump the fee of this transaction by performing a
	// CPFP from Alice's point of view.
	bumpFeeReq := &walletrpc.BumpFeeRequest{
		Outpoint:   op,
		SatPerByte: uint32(sweep.DefaultMaxFeeRate.FeePerKVByte() / 2000),
	}
	ctxt, _ = context.WithTimeout(ctxb, defaultTimeout)
	_, err = net.Bob.WalletKitClient.BumpFee(ctxt, bumpFeeReq)
	if err != nil {
		t.Fatalf("unable to bump fee: %v", err)
	}

	// We should now expect to see two transactions within the mempool, a
	// parent and its child.
	_, err = waitForNTxsInMempool(net.Miner.Node, 2, minerMempoolTimeout)
	if err != nil {
		t.Fatalf("expected two mempool transactions: %v", err)
	}

	// We should also expect to see the output being swept by the
	// UtxoSweeper. We'll ensure it's using the fee rate specified.
	pendingSweepsReq := &walletrpc.PendingSweepsRequest{}
	ctxt, _ = context.WithTimeout(ctxb, defaultTimeout)
	pendingSweepsResp, err := net.Bob.WalletKitClient.PendingSweeps(
		ctxt, pendingSweepsReq,
	)
	if err != nil {
		t.Fatalf("unable to retrieve pending sweeps: %v", err)
	}
	if len(pendingSweepsResp.PendingSweeps) != 1 {
		t.Fatalf("expected to find %v pending sweep(s), found %v", 1,
			len(pendingSweepsResp.PendingSweeps))
	}
	pendingSweep := pendingSweepsResp.PendingSweeps[0]
	if !bytes.Equal(pendingSweep.Outpoint.TxidBytes, op.TxidBytes) {
		t.Fatalf("expected output txid %x, got %x", op.TxidBytes,
			pendingSweep.Outpoint.TxidBytes)
	}
	if pendingSweep.Outpoint.OutputIndex != op.OutputIndex {
		t.Fatalf("expected output index %v, got %v", op.OutputIndex,
			pendingSweep.Outpoint.OutputIndex)
	}
	if pendingSweep.SatPerByte != bumpFeeReq.SatPerByte {
		t.Fatalf("expected sweep sat per byte %v, got %v",
			bumpFeeReq.SatPerByte, pendingSweep.SatPerByte)
	}

	// Mine a block to clean up the unconfirmed transactions.
	mineBlocks(t, net, 1, 2)

	// The input used to CPFP should no longer be pending.
	err = wait.NoError(func() error {
		req := &walletrpc.PendingSweepsRequest{}
		ctxt, _ = context.WithTimeout(ctxb, defaultTimeout)
		resp, err := net.Bob.WalletKitClient.PendingSweeps(ctxt, req)
		if err != nil {
			return fmt.Errorf("unable to retrieve bob's pending "+
				"sweeps: %v", err)
		}
		if len(resp.PendingSweeps) != 0 {
			return fmt.Errorf("expected 0 pending sweeps, found %d",
				len(resp.PendingSweeps))
		}
		return nil
	}, defaultTimeout)
	if err != nil {
		t.Fatalf(err.Error())
	}
}

// testGetRecoveryInfo checks whether lnd gives the right information about
// the wallet recovery process.
func testGetRecoveryInfo(net *lntest.NetworkHarness, t *harnessTest) {
	ctxb := context.Background()

	// First, create a new node with strong passphrase and grab the mnemonic
	// used for key derivation. This will bring up Carol with an empty
	// wallet, and such that she is synced up.
	password := []byte("The Magic Words are Squeamish Ossifrage")
	carol, mnemonic, _, err := net.NewNodeWithSeed(
		"Carol", nil, password, false,
	)
	if err != nil {
		t.Fatalf("unable to create node with seed; %v", err)
	}

	shutdownAndAssert(net, t, carol)

	checkInfo := func(expectedRecoveryMode, expectedRecoveryFinished bool,
		expectedProgress float64, recoveryWindow int32) {

		// Restore Carol, passing in the password, mnemonic, and
		// desired recovery window.
		node, err := net.RestoreNodeWithSeed(
			"Carol", nil, password, mnemonic, recoveryWindow, nil,
		)
		if err != nil {
			t.Fatalf("unable to restore node: %v", err)
		}

		// Wait for Carol to sync to the chain.
		_, minerHeight, err := net.Miner.Node.GetBestBlock()
		if err != nil {
			t.Fatalf("unable to get current blockheight %v", err)
		}
		ctxt, _ := context.WithTimeout(ctxb, defaultTimeout)
		err = waitForNodeBlockHeight(ctxt, node, minerHeight)
		if err != nil {
			t.Fatalf("unable to sync to chain: %v", err)
		}

		// Query carol for her current wallet recovery progress.
		var (
			recoveryMode     bool
			recoveryFinished bool
			progress         float64
		)

		err = wait.Predicate(func() bool {
			// Verify that recovery info gives the right response.
			req := &lnrpc.GetRecoveryInfoRequest{}
			ctxt, _ := context.WithTimeout(ctxb, defaultTimeout)
			resp, err := node.GetRecoveryInfo(ctxt, req)
			if err != nil {
				t.Fatalf("unable to query recovery info: %v", err)
			}

			recoveryMode = resp.RecoveryMode
			recoveryFinished = resp.RecoveryFinished
			progress = resp.Progress

			if recoveryMode != expectedRecoveryMode ||
				recoveryFinished != expectedRecoveryFinished ||
				progress != expectedProgress {
				return false
			}

			return true
		}, 15*time.Second)
		if err != nil {
			t.Fatalf("expected recovery mode to be %v, got %v, "+
				"expected recovery finished to be %v, got %v, "+
				"expected progress %v, got %v",
				expectedRecoveryMode, recoveryMode,
				expectedRecoveryFinished, recoveryFinished,
				expectedProgress, progress,
			)
		}

		// Lastly, shutdown this Carol so we can move on to the next
		// restoration.
		shutdownAndAssert(net, t, node)
	}

	// Restore Carol with a recovery window of 0. Since it's not in recovery
	// mode, the recovery info will give a response with recoveryMode=false,
	// recoveryFinished=false, and progress=0
	checkInfo(false, false, 0, 0)

	// Change the recovery windown to be 1 to turn on recovery mode. Since the
	// current chain height is the same as the birthday height, it should
	// indicate the recovery process is finished.
	checkInfo(true, true, 1, 1)

	// We now go ahead 5 blocks. Because the wallet's syncing process is
	// controlled by a goroutine in the background, it will catch up quickly.
	// This makes the recovery progress back to 1.
	mineBlocks(t, net, 5, 0)
	checkInfo(true, true, 1, 1)
}

// testOnchainFundRecovery checks lnd's ability to rescan for onchain outputs
// when providing a valid aezeed that owns outputs on the chain. This test
// performs multiple restorations using the same seed and various recovery
// windows to ensure we detect funds properly.
func testOnchainFundRecovery(net *lntest.NetworkHarness, t *harnessTest) {
	ctxb := context.Background()

	// First, create a new node with strong passphrase and grab the mnemonic
	// used for key derivation. This will bring up Carol with an empty
	// wallet, and such that she is synced up.
	password := []byte("The Magic Words are Squeamish Ossifrage")
	carol, mnemonic, _, err := net.NewNodeWithSeed(
		"Carol", nil, password, false,
	)
	require.NoError(t.t, err)
	shutdownAndAssert(net, t, carol)

	// Create a closure-factory for building closures that can generate and
	// skip a configurable number of addresses, before finally sending coins
	// to a next generated address. The returned closure will apply the same
	// behavior to both default P2WKH and NP2WKH scopes.
	skipAndSend := func(nskip int) func(*lntest.HarnessNode) {
		return func(node *lntest.HarnessNode) {
			newP2WKHAddrReq := &lnrpc.NewAddressRequest{
				Type: AddrTypeWitnessPubkeyHash,
			}

			newNP2WKHAddrReq := &lnrpc.NewAddressRequest{
				Type: AddrTypeNestedPubkeyHash,
			}

			// Generate and skip the number of addresses requested.
			for i := 0; i < nskip; i++ {
				ctxt, _ := context.WithTimeout(ctxb, defaultTimeout)
				_, err = node.NewAddress(ctxt, newP2WKHAddrReq)
				if err != nil {
					t.Fatalf("unable to generate new "+
						"p2wkh address: %v", err)
				}

				ctxt, _ = context.WithTimeout(ctxb, defaultTimeout)
				_, err = node.NewAddress(ctxt, newNP2WKHAddrReq)
				if err != nil {
					t.Fatalf("unable to generate new "+
						"np2wkh address: %v", err)
				}
			}

			// Send one BTC to the next P2WKH address.
			ctxt, _ := context.WithTimeout(ctxb, defaultTimeout)
			err = net.SendCoins(
				ctxt, btcutil.SatoshiPerBitcoin, node,
			)
			if err != nil {
				t.Fatalf("unable to send coins to node: %v",
					err)
			}

			// And another to the next NP2WKH address.
			ctxt, _ = context.WithTimeout(ctxb, defaultTimeout)
			err = net.SendCoinsNP2WKH(
				ctxt, btcutil.SatoshiPerBitcoin, node,
			)
			if err != nil {
				t.Fatalf("unable to send coins to node: %v",
					err)
			}
		}
	}

	// Restore Carol with a recovery window of 0. Since no coins have been
	// sent, her balance should be zero.
	//
	// After, one BTC is sent to both her first external P2WKH and NP2WKH
	// addresses.
	restoreCheckBalance(t, password, mnemonic, 0, 0, 0, skipAndSend(0))

	// Check that restoring without a look-ahead results in having no funds
	// in the wallet, even though they exist on-chain.
	restoreCheckBalance(t, password, mnemonic, 0, 0, 0, nil)

	// Now, check that using a look-ahead of 1 recovers the balance from
	// the two transactions above. We should also now have 2 UTXOs in the
	// wallet at the end of the recovery attempt.
	//
	// After, we will generate and skip 9 P2WKH and NP2WKH addresses, and
	// send another BTC to the subsequent 10th address in each derivation
	// path.
	restoreCheckBalance(
		t, password, mnemonic, 2*btcutil.SatoshiPerBitcoin, 2, 1,
		skipAndSend(9),
	)

	// Check that using a recovery window of 9 does not find the two most
	// recent txns.
	restoreCheckBalance(
		t, password, mnemonic, 2*btcutil.SatoshiPerBitcoin, 2, 9, nil,
	)

	// Extending our recovery window to 10 should find the most recent
	// transactions, leaving the wallet with 4 BTC total. We should also
	// learn of the two additional UTXOs created above.
	//
	// After, we will skip 19 more addrs, sending to the 20th address past
	// our last found address, and repeat the same checks.
	restoreCheckBalance(
		t, password, mnemonic, 4*btcutil.SatoshiPerBitcoin, 4, 10,
		skipAndSend(19),
	)

	// Check that recovering with a recovery window of 19 fails to find the
	// most recent transactions.
	restoreCheckBalance(
		t, password, mnemonic, 4*btcutil.SatoshiPerBitcoin, 4, 19, nil,
	)

	// Ensure that using a recovery window of 20 succeeds with all UTXOs
	// found and the final balance reflected.

	// After these checks are done, we'll want to make sure we can also
	// recover change address outputs.  This is mainly motivated by a now
	// fixed bug in the wallet in which change addresses could at times be
	// created outside of the default key scopes. Recovery only used to be
	// performed on the default key scopes, so ideally this test case
	// would've caught the bug earlier. Carol has received 6 BTC so far from
	// the miner, we'll send 5 back to ensure all of her UTXOs get spent to
	// avoid fee discrepancies and a change output is formed.
	const minerAmt = 5 * btcutil.SatoshiPerBitcoin
	const finalBalance = 6 * btcutil.SatoshiPerBitcoin
	promptChangeAddr := func(node *lntest.HarnessNode) {
		minerAddr, err := net.Miner.NewAddress()
		if err != nil {
			t.Fatalf("unable to create new miner address: %v", err)
		}
		ctxt, _ := context.WithTimeout(ctxb, defaultTimeout)
		resp, err := node.SendCoins(ctxt, &lnrpc.SendCoinsRequest{
			Addr:   minerAddr.String(),
			Amount: minerAmt,
		})
		if err != nil {
			t.Fatalf("unable to send coins to miner: %v", err)
		}
		txid, err := waitForTxInMempool(
			net.Miner.Node, minerMempoolTimeout,
		)
		if err != nil {
			t.Fatalf("transaction not found in mempool: %v", err)
		}
		if resp.Txid != txid.String() {
			t.Fatalf("txid mismatch: %v vs %v", resp.Txid,
				txid.String())
		}
		block := mineBlocks(t, net, 1, 1)[0]
		assertTxInBlock(t, block, txid)
	}
	restoreCheckBalance(
		t, password, mnemonic, finalBalance, 6, 20, promptChangeAddr,
	)

	// We should expect a static fee of 27750 satoshis for spending 6 inputs
	// (3 P2WPKH, 3 NP2WPKH) to two P2WPKH outputs. Carol should therefore
	// only have one UTXO present (the change output) of 6 - 5 - fee BTC.
	const fee = 27750
	restoreCheckBalance(
		t, password, mnemonic, finalBalance-minerAmt-fee, 1, 21, nil,
	)
}

// testSweepAllCoins tests that we're able to properly sweep all coins from the
// wallet into a single target address at the specified fee rate.
func testSweepAllCoins(net *lntest.NetworkHarness, t *harnessTest) {
	ctxb := context.Background()

	// First, we'll make a new node, ainz who'll we'll use to test wallet
	// sweeping.
	ainz, err := net.NewNode("Ainz", nil)
	if err != nil {
		t.Fatalf("unable to create new node: %v", err)
	}
	defer shutdownAndAssert(net, t, ainz)

	// Next, we'll give Ainz exactly 2 utxos of 1 BTC each, with one of
	// them being p2wkh and the other being a n2wpkh address.
	ctxt, _ := context.WithTimeout(ctxb, defaultTimeout)
	err = net.SendCoins(ctxt, btcutil.SatoshiPerBitcoin, ainz)
	if err != nil {
		t.Fatalf("unable to send coins to eve: %v", err)
	}
	ctxt, _ = context.WithTimeout(ctxb, defaultTimeout)
	err = net.SendCoinsNP2WKH(ctxt, btcutil.SatoshiPerBitcoin, ainz)
	if err != nil {
		t.Fatalf("unable to send coins to eve: %v", err)
	}

	// Ensure that we can't send coins to our own Pubkey.
	info, err := ainz.GetInfo(ctxt, &lnrpc.GetInfoRequest{})
	if err != nil {
		t.Fatalf("unable to get node info: %v", err)
	}

	// Create a label that we will used to label the transaction with.
	sendCoinsLabel := "send all coins"

	sweepReq := &lnrpc.SendCoinsRequest{
		Addr:    info.IdentityPubkey,
		SendAll: true,
		Label:   sendCoinsLabel,
	}
	_, err = ainz.SendCoins(ctxt, sweepReq)
	if err == nil {
		t.Fatalf("expected SendCoins to users own pubkey to fail")
	}

	// Ensure that we can't send coins to another users Pubkey.
	info, err = net.Alice.GetInfo(ctxt, &lnrpc.GetInfoRequest{})
	if err != nil {
		t.Fatalf("unable to get node info: %v", err)
	}

	sweepReq = &lnrpc.SendCoinsRequest{
		Addr:    info.IdentityPubkey,
		SendAll: true,
		Label:   sendCoinsLabel,
	}
	_, err = ainz.SendCoins(ctxt, sweepReq)
	if err == nil {
		t.Fatalf("expected SendCoins to Alices pubkey to fail")
	}

	// With the two coins above mined, we'll now instruct ainz to sweep all
	// the coins to an external address not under its control.
	// We will first attempt to send the coins to addresses that are not
	// compatible with the current network. This is to test that the wallet
	// will prevent any onchain transactions to addresses that are not on the
	// same network as the user.

	// Send coins to a testnet3 address.
	ctxt, _ = context.WithTimeout(ctxb, defaultTimeout)
	sweepReq = &lnrpc.SendCoinsRequest{
		Addr:    "tb1qfc8fusa98jx8uvnhzavxccqlzvg749tvjw82tg",
		SendAll: true,
		Label:   sendCoinsLabel,
	}
	_, err = ainz.SendCoins(ctxt, sweepReq)
	if err == nil {
		t.Fatalf("expected SendCoins to different network to fail")
	}

	// Send coins to a mainnet address.
	ctxt, _ = context.WithTimeout(ctxb, defaultTimeout)
	sweepReq = &lnrpc.SendCoinsRequest{
		Addr:    "1MPaXKp5HhsLNjVSqaL7fChE3TVyrTMRT3",
		SendAll: true,
		Label:   sendCoinsLabel,
	}
	_, err = ainz.SendCoins(ctxt, sweepReq)
	if err == nil {
		t.Fatalf("expected SendCoins to different network to fail")
	}

	// Send coins to a compatible address.
	minerAddr, err := net.Miner.NewAddress()
	if err != nil {
		t.Fatalf("unable to create new miner addr: %v", err)
	}

	sweepReq = &lnrpc.SendCoinsRequest{
		Addr:    minerAddr.String(),
		SendAll: true,
		Label:   sendCoinsLabel,
	}
	ctxt, _ = context.WithTimeout(ctxb, defaultTimeout)
	_, err = ainz.SendCoins(ctxt, sweepReq)
	if err != nil {
		t.Fatalf("unable to sweep coins: %v", err)
	}

	// We'll mine a block which should include the sweep transaction we
	// generated above.
	block := mineBlocks(t, net, 1, 1)[0]

	// The sweep transaction should have exactly two inputs as we only had
	// two UTXOs in the wallet.
	sweepTx := block.Transactions[1]
	if len(sweepTx.TxIn) != 2 {
		t.Fatalf("expected 2 inputs instead have %v", len(sweepTx.TxIn))
	}

	sweepTxStr := sweepTx.TxHash().String()
	assertTxLabel(ctxb, t, ainz, sweepTxStr, sendCoinsLabel)

	// While we are looking at labels, we test our label transaction command
	// to make sure it is behaving as expected. First, we try to label our
	// transaction with an empty label, and check that we fail as expected.
	sweepHash := sweepTx.TxHash()
	_, err = ainz.WalletKitClient.LabelTransaction(
		ctxt, &walletrpc.LabelTransactionRequest{
			Txid:      sweepHash[:],
			Label:     "",
			Overwrite: false,
		},
	)
	if err == nil {
		t.Fatalf("expected error for zero transaction label")
	}

	// Our error will be wrapped in a rpc error, so we check that it
	// contains the error we expect.
	errZeroLabel := "cannot label transaction with empty label"
	if !strings.Contains(err.Error(), errZeroLabel) {
		t.Fatalf("expected: zero label error, got: %v", err)
	}

	// Next, we try to relabel our transaction without setting the overwrite
	// boolean. We expect this to fail, because the wallet requires setting
	// of this param to prevent accidental overwrite of labels.
	_, err = ainz.WalletKitClient.LabelTransaction(
		ctxt, &walletrpc.LabelTransactionRequest{
			Txid:      sweepHash[:],
			Label:     "label that will not work",
			Overwrite: false,
		},
	)
	if err == nil {
		t.Fatalf("expected error for tx already labelled")
	}

	// Our error will be wrapped in a rpc error, so we check that it
	// contains the error we expect.
	if !strings.Contains(err.Error(), wallet.ErrTxLabelExists.Error()) {
		t.Fatalf("expected: label exists, got: %v", err)
	}

	// Finally, we overwrite our label with a new label, which should not
	// fail.
	newLabel := "new sweep tx label"
	_, err = ainz.WalletKitClient.LabelTransaction(
		ctxt, &walletrpc.LabelTransactionRequest{
			Txid:      sweepHash[:],
			Label:     newLabel,
			Overwrite: true,
		},
	)
	if err != nil {
		t.Fatalf("could not label tx: %v", err)
	}

	assertTxLabel(ctxb, t, ainz, sweepTxStr, newLabel)

	// Finally, Ainz should now have no coins at all within his wallet.
	balReq := &lnrpc.WalletBalanceRequest{}
	resp, err := ainz.WalletBalance(ctxt, balReq)
	if err != nil {
		t.Fatalf("unable to get ainz's balance: %v", err)
	}
	switch {
	case resp.ConfirmedBalance != 0:
		t.Fatalf("expected no confirmed balance, instead have %v",
			resp.ConfirmedBalance)

	case resp.UnconfirmedBalance != 0:
		t.Fatalf("expected no unconfirmed balance, instead have %v",
			resp.UnconfirmedBalance)
	}

	// If we try again, but this time specifying an amount, then the call
	// should fail.
	sweepReq.Amount = 10000
	_, err = ainz.SendCoins(ctxt, sweepReq)
	if err == nil {
		t.Fatalf("sweep attempt should fail")
	}
}

// Create a closure for testing the recovery of Carol's wallet. This
// method takes the expected value of Carol's balance when using the
// given recovery window. Additionally, the caller can specify an action
// to perform on the restored node before the node is shutdown.
func restoreCheckBalance(t *harnessTest, password []byte, mnemonic []string,
	expAmount int64, expectedNumUTXOs uint32,
	recoveryWindow int32, fn func(*lntest.HarnessNode)) {

	t.t.Helper()
	
	ctxb := context.Background()

	// Restore Carol, passing in the password, mnemonic, and
	// desired recovery window.
	node, err := t.lndHarness.RestoreNodeWithSeed(
		"Carol", nil, password, mnemonic, recoveryWindow, nil,
	)
	require.NoError(t.t, err)

	// Query carol for her current wallet balance, and also that we
	// gain the expected number of UTXOs.
	var (
		currBalance  int64
		currNumUTXOs uint32
	)
	err = wait.Predicate(func() bool {
		req := &lnrpc.WalletBalanceRequest{}
		ctxt, _ := context.WithTimeout(ctxb, defaultTimeout)
		resp, err := node.WalletBalance(ctxt, req)
		require.NoError(t.t, err)
		currBalance = resp.ConfirmedBalance

		utxoReq := &lnrpc.ListUnspentRequest{
			MaxConfs: math.MaxInt32,
		}
		ctxt, _ = context.WithTimeout(ctxb, defaultTimeout)
		utxoResp, err := node.ListUnspent(ctxt, utxoReq)
		require.NoError(t.t, err)
		currNumUTXOs = uint32(len(utxoResp.Utxos))

		// Verify that Carol's balance and number of UTXOs
		// matches what's expected.
		if expAmount != currBalance {
			return false
		}
		if currNumUTXOs != expectedNumUTXOs {
			return false
		}

		return true
	}, defaultTimeout)
	require.NoError(
		t.t, err, "expected restored node to have %d satoshis, "+
			"instead has %d satoshis, expected %d utxos "+
			"instead has %d", expAmount, currBalance,
		expectedNumUTXOs, currNumUTXOs,
	)

	// If the user provided a callback, execute the commands against
	// the restored Carol.
	if fn != nil {
		fn(node)
	}

	// Lastly, shutdown this Carol so we can move on to the next
	// restoration.
	shutdownAndAssert(t.lndHarness, t, node)
}
