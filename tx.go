package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	cli "github.com/jawher/mow.cli"
	log "github.com/xlab/suplog"

	"github.com/xlab/etherman/deployer"
)

func onTx(cmd *cli.Cmd) {
	bytecodeOnly := cmd.BoolOpt("bytecode", false, "Produce hex-encoded ABI-packed calldata bytecode only. Do not interact with RPC.")
	contractAddress := cmd.StringArg("ADDRESS", "", "Contract address to interact with.")
	methodName := cmd.StringArg("METHOD", "", "Contract method to transact.")
	methodArgs := cmd.StringsArg("ARGS", []string{}, "Method transaction arguments. Will be ABI-encoded.")
	await := cmd.BoolOpt("await", true, "Await transaction confirmation from the RPC.")

	cmd.Spec = "[--bytecode | --await] ADDRESS METHOD [ARGS...]"

	cmd.Action = func() {
		d, err := deployer.New(
			deployer.OptionRPCTimeout(duration(*rpcTimeout, defaultRPCTimeout)),
			deployer.OptionCallTimeout(duration(*callTimeout, defaultCallTimeout)),
			deployer.OptionTxTimeout(duration(*txTimeout, defaultTxTimeout)),

			// only options applicable to tx
			deployer.OptionEVMRPCEndpoint(*evmEndpoint),
			deployer.OptionGasPrice(big.NewInt(int64(*gasPrice))),
			deployer.OptionGasLimit(uint64(*gasLimit)),
			deployer.OptionNoCache(*noCache),
			deployer.OptionBuildCacheDir(*buildCacheDir),
			deployer.OptionSolcAllowedPaths(*solAllowedPaths),
			deployer.OptionEnableCoverage(*coverage),
		)
		if err != nil {
			log.WithError(err).Fatalln("failed to init deployer")
		}

		client, err := d.Backend()
		if err != nil {
			log.Fatalln(err)
		}

		chainCtx, cancelFn := context.WithTimeout(context.Background(), duration(*rpcTimeout, defaultRPCTimeout))
		defer cancelFn()

		chainID, err := client.ChainID(chainCtx)
		if err != nil {
			log.WithError(err).Fatalln("failed get valid chain ID")
		}

		fromAddress, signerFn, err := initEthereumAccountsManager(
			chainID.Uint64(),
			keystoreDir,
			from,
			fromPassphrase,
			fromPrivKey,
			useLedger,
		)
		if err != nil {
			log.WithError(err).Fatalln("failed init SignerFn")
		}

		log.Debugln("sending from", fromAddress.Hex())

		txOpts := deployer.ContractTxOpts{
			From:         fromAddress,
			SignerFn:     signerFn,
			SolSource:    *solSource,
			ContractName: *contractName,
			Contract:     common.HexToAddress(*contractAddress),
			BytecodeOnly: *bytecodeOnly,
			Await:        *await,
		}
		if *coverage {
			txOpts.CoverageAgent = deployer.NewCoverageDataCollector(deployer.CoverageModeDefault)
		}

		log.Debugln("sending from", fromAddress.Hex())
		log.Debugln("target contract", txOpts.Contract.Hex())

		txHash, abiPackedCalldata, err := d.Tx(
			context.Background(),
			txOpts,
			*methodName,
			func(args abi.Arguments) []interface{} {
				mappedArgs, err := mapStringArgs(args, *methodArgs)
				if err != nil {
					log.WithError(err).Fatalln("failed to map method args")
					return nil
				}

				return mappedArgs
			},
		)
		if err != nil {
			log.Fatalln(err)
		}

		if *bytecodeOnly {
			fmt.Println(hex.EncodeToString(abiPackedCalldata))
			return
		}

		if !*await {
			log.WithField("contract", txOpts.Contract.Hex()).Infoln("sent tx", txHash.Hex())
		}

		fmt.Println(txHash.Hex())
	}
}
