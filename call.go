package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	cli "github.com/jawher/mow.cli"
	log "github.com/xlab/suplog"

	"github.com/xlab/etherman/deployer"
)

func onCall(cmd *cli.Cmd) {
	bytecodeOnly := cmd.BoolOpt("bytecode", false, "Produce hex-encoded ABI-packed calldata bytecode only. Do not interact with RPC.")
	contractAddress := cmd.StringArg("ADDRESS", "", "Contract address to interact with.")
	methodName := cmd.StringArg("METHOD", "", "Contract method to transact.")
	methodArgs := cmd.StringsArg("ARGS", []string{}, "Method transaction arguments. Will be ABI-encoded.")
	fromAddress := cmd.StringOpt("from", "0x0000000000000000000000000000000000000000", "Estimate transaction using specified from address.")

	cmd.Spec = "[--bytecode] [--from] ADDRESS METHOD [ARGS...]"

	cmd.Action = func() {
		d, err := deployer.New(
			deployer.OptionRPCTimeout(duration(*rpcTimeout, defaultRPCTimeout)),
			deployer.OptionCallTimeout(duration(*callTimeout, defaultCallTimeout)),
			deployer.OptionTxTimeout(duration(*txTimeout, defaultTxTimeout)),

			// only options applicable to call
			deployer.OptionEVMRPCEndpoint(*evmEndpoint),
			deployer.OptionNoCache(*noCache),
			deployer.OptionBuildCacheDir(*buildCacheDir),
			deployer.OptionSolcAllowedPaths(*solAllowedPaths),
			deployer.OptionEnableCoverage(*coverage),
		)
		if err != nil {
			log.WithError(err).Fatalln("failed to init deployer")
		}

		callOpts := deployer.ContractCallOpts{
			From:         common.HexToAddress(*fromAddress),
			SolSource:    *solSource,
			ContractName: *contractName,
			Contract:     common.HexToAddress(*contractAddress),
		}
		if *coverage {
			callOpts.CoverageAgent = deployer.NewCoverageDataCollector(deployer.CoverageModeDefault)

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

			callOpts.From = fromAddress
			callOpts.CoverageCall.SignerFn = signerFn
		}

		log.Debugln("target contract", callOpts.Contract.Hex())
		log.Debugln("using from address", callOpts.From.Hex())

		output, _, err := d.Call(
			context.Background(),
			callOpts,
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
			fmt.Println(hex.EncodeToString(output[0].([]byte)))
			return
		}

		v, _ := json.MarshalIndent(output, "", "\t")
		fmt.Println(string(v))
	}
}
