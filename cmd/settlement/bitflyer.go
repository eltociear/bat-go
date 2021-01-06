package settlement

import (
	"context"
	"encoding/json"
	"errors"
	"io/ioutil"
	"path/filepath"
	"strings"

	"github.com/brave-intl/bat-go/cmd"
	"github.com/brave-intl/bat-go/settlement"
	bitflyersettlement "github.com/brave-intl/bat-go/settlement/bitflyer"
	"github.com/brave-intl/bat-go/utils/clients/bitflyer"
	appctx "github.com/brave-intl/bat-go/utils/context"
	"github.com/brave-intl/bat-go/utils/logging"
	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"
)

/*
plan
hostname:  http://demo22oy5z2d2lu6pyoum26m7k.azurewebsites.net
client_id: 6cd6f1a070afcd467e198c8039b2c97b

account/inventory
v
getprice?product_code=BAT_JPY
v
withdraw-to-deposit-id/request
v
withdraw-to-deposit-id/status


*/

var (
	// BitflyerSettlementCmd creates the bitflyer subcommand
	BitflyerSettlementCmd = &cobra.Command{
		Use:   "bitflyer",
		Short: "facilitates bitflyer settlement",
	}

	// UploadBitflyerSettlementCmd creates the bitflyer uphold subcommand
	UploadBitflyerSettlementCmd = &cobra.Command{
		Use:   "upload",
		Short: "uploads signed bitflyer transactions",
		Run:   cmd.Perform("bitflyer upload", UploadBitflyerSettlement),
	}

	// CheckStatusBitflyerSettlementCmd creates the bitflyer checkstatus subcommand
	CheckStatusBitflyerSettlementCmd = &cobra.Command{
		Use:   "checkstatus",
		Short: "uploads signed bitflyer transactions",
		Run:   cmd.Perform("bitflyer checkstatus", CheckStatusBitflyerSettlement),
	}
)

// UploadBitflyerSettlement uploads bitflyer settlement
func UploadBitflyerSettlement(cmd *cobra.Command, args []string) error {
	input, err := cmd.Flags().GetString("input")
	if err != nil {
		return err
	}
	sig, err := cmd.Flags().GetInt("sig")
	if err != nil {
		return err
	}
	allTransactionsFile, err := cmd.Flags().GetString("all-txs-input")
	if err != nil {
		return err
	}
	out, err := cmd.Flags().GetString("out")
	if err != nil {
		return err
	}

	if out == "" {
		out = strings.TrimSuffix(input, filepath.Ext(input)) + "-finished.json"
	}
	return BitflyerUploadSettlement(
		cmd.Context(),
		"upload",
		input,
		sig,
		allTransactionsFile,
		out,
	)
}

// CheckStatusBitflyerSettlement is the command runner for checking bitflyer transactions status
func CheckStatusBitflyerSettlement(cmd *cobra.Command, args []string) error {
	input, err := cmd.Flags().GetString("input")
	if err != nil {
		return err
	}
	out, err := cmd.Flags().GetString("out")
	if err != nil {
		return err
	}
	if out == "" {
		out = strings.TrimSuffix(input, filepath.Ext(input)) + "-finished.json"
	}
	sig, err := cmd.Flags().GetInt("sig")
	if err != nil {
		return err
	}
	allTxsInput, err := cmd.Flags().GetString("all-txs-input")
	if err != nil {
		return err
	}
	return BitflyerUploadSettlement(
		cmd.Context(),
		"checkstatus",
		input,
		sig,
		allTxsInput,
		out,
	)
}

func init() {
	// add complete and transform subcommand
	BitflyerSettlementCmd.AddCommand(UploadBitflyerSettlementCmd)
	BitflyerSettlementCmd.AddCommand(CheckStatusBitflyerSettlementCmd)

	// add this command as a settlement subcommand
	SettlementCmd.AddCommand(BitflyerSettlementCmd)

	// setup the flags
	uploadCheckStatusBuilder := cmd.NewFlagBuilder(UploadBitflyerSettlementCmd).
		AddCommand(CheckStatusBitflyerSettlementCmd)

	uploadCheckStatusBuilder.Flag().String("input", "",
		"the file or comma delimited list of files that should be utilized. both referrals and contributions should be done in one command in order to group the transactions appropriately").
		Require().
		Bind("input").
		Env("INPUT")

	uploadCheckStatusBuilder.Flag().String("out", "./bitflyer-settlement",
		"the location of the file").
		Bind("out").
		Env("OUT")

	uploadCheckStatusBuilder.Flag().String("all-txs-input", "",
		"the original transactions file").
		Bind("all-txs-input").
		Require()
}

// BitflyerUploadSettlement marks the settlement file as complete
func BitflyerUploadSettlement(ctx context.Context, action string, inPath string, signatureSwitch int, allTransactionsFile string, outPath string) error {
	logger, err := appctx.GetLogger(ctx)
	if err != nil {
		_, logger = logging.SetupLogger(ctx)
	}

	if outPath == "./bitflyer-settlement" {
		// use a file with extension if none is passed
		outPath = "./bitflyer-settlement-complete.json"
	}

	bulkPayoutFiles := strings.Split(inPath, ",")
	bitflyerClient, err := bitflyer.New()
	if err != nil {
		logger.Error().Err(err).Msg("failed to create new bitflyer client")
		return err
	}

	if allTransactionsFile == "" {
		logger.Error().Msg("transactions file is empty")
		return errors.New("unable to upload without a transactions file to check against")
	}

	bytes, err := ioutil.ReadFile(allTransactionsFile)
	if err != nil {
		logger.Error().Err(err).Msg("failed to read the transactions file")
		return err
	}

	var settlementTransactions []settlement.AntifraudTransaction
	err = json.Unmarshal(bytes, &settlementTransactions)
	if err != nil {
		logger.Error().Err(err).Msg("failed to unmarshal the transactions file")
		return err
	}
	// create a map of the request transactions
	transactionsMap := bitflyerMapTransactionsToID(settlementTransactions)

	submittedTransactions, submitErr := bitflyersettlement.IterateRequest(
		ctx,
		action,
		bitflyerClient,
		signatureSwitch,
		bulkPayoutFiles,
		transactionsMap,
	)
	// write file for upload to eyeshade
	logger.Info().
		Str("files", outPath).
		Msg("outputting files")

	if submittedTransactions != nil {
		for key, txs := range *submittedTransactions {
			outputPath := strings.TrimSuffix(outPath, filepath.Ext(outPath)) + "-" + key + ".json"
			err = BitflyerWriteTransactions(ctx, outputPath, &txs)
			if err != nil {
				logger.Error().Err(err).Msg("failed to write bitflyer transactions file")
				return err
			}
		}
	}
	return submitErr
}

// bitflyerMapTransactionsToID creates a map of guid's to transactions
func bitflyerMapTransactionsToID(antifraudTransactions []settlement.AntifraudTransaction) (
	map[string]settlement.Transaction,
	[]settlement.Transaction,
) {
	transactionsMap := make(map[string]settlement.Transaction)
	settlementTransactions := []settlement.Transaction{}
	for _, atx := range antifraudTransactions {
		tx := atx.ToTransaction()
		transferID := bitflyer.GenerateTransferID(&tx)
		previousTx := transactionsMap[transferID]
		if previousTx.Amount.Equals(decimal.NewFromFloat32(0)) {
			// add tx to map
			previousTx = tx
		} else {
			// add amount to map
			previousTx.Amount = previousTx.Amount.Add(tx.Amount)
		}
		// check that we are not above amount limits on eyeshade
		amountLimit := decimal.NewFromFloat32(20000)
		if previousTx.Amount.LessThanOrEqual(amountLimit) {
			// within the bounds of our per publishers limit
			// ok to settle full amount, as is
			settlementTransactions = append(settlementTransactions, tx)
		} else {
			// over the bounds provided. will have to
			difference := previousTx.Amount.Sub(amountLimit)
			if difference.LessThan(tx.Amount) {
				// partial settlement on eyeshade side
				tx.Amount = tx.Amount.Sub(difference)
				settlementTransactions = append(settlementTransactions, tx)
			} else {
				// do nothing
				// completely outside of the bounds. do not settle on eyeshade side
			}
			previousTx.Amount = amountLimit
		}
		transactionsMap[transferID] = previousTx
	}
	return transactionsMap, settlementTransactions
}

// BitflyerWriteTransactions writes settlement transactions to a json file
func BitflyerWriteTransactions(ctx context.Context, outPath string, metadata *[]settlement.Transaction) error {
	logger, err := appctx.GetLogger(ctx)
	if err != nil {
		_, logger = logging.SetupLogger(ctx)
	}

	if len(*metadata) == 0 {
		return nil
	}

	logger.Debug().Str("files", outPath).Int("num transactions", len(*metadata)).Msg("writing outputting files")
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		logger.Error().Err(err).Msg("failed writing outputting files")
		return err
	}
	return ioutil.WriteFile(outPath, data, 0600)
}

// BitflyerWriteRequests writes settlement transactions to a json file
func BitflyerWriteRequests(outPath string, metadata *[][]bitflyer.PayoutPayload) error {
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(outPath, data, 0600)
}