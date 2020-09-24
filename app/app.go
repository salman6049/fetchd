package app

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	bam "github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/server/api"
	"github.com/cosmos/cosmos-sdk/simapp"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/cosmos/cosmos-sdk/version"
	"github.com/cosmos/cosmos-sdk/x/auth"
	authrest "github.com/cosmos/cosmos-sdk/x/auth/client/rest"
	authvesting "github.com/cosmos/cosmos-sdk/x/auth/vesting"
	"github.com/cosmos/cosmos-sdk/x/bank"
	"github.com/cosmos/cosmos-sdk/x/crisis"
	distr "github.com/cosmos/cosmos-sdk/x/distribution"
	"github.com/cosmos/cosmos-sdk/x/evidence"
	"github.com/cosmos/cosmos-sdk/x/genutil"
	"github.com/cosmos/cosmos-sdk/x/gov"
	"github.com/cosmos/cosmos-sdk/x/mint"
	"github.com/cosmos/cosmos-sdk/x/params"
	paramsclient "github.com/cosmos/cosmos-sdk/x/params/client"
	"github.com/cosmos/cosmos-sdk/x/slashing"
	"github.com/cosmos/cosmos-sdk/x/staking"
	"github.com/cosmos/cosmos-sdk/x/supply"
	"github.com/cosmos/cosmos-sdk/x/upgrade"
	upgradeclient "github.com/cosmos/cosmos-sdk/x/upgrade/client"

	"github.com/fetchai/fetchd/x/wasm"
	wasmclient "github.com/fetchai/fetchd/x/wasm/client"

	"github.com/spf13/viper"
	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/libs/cli"
	"github.com/tendermint/tendermint/libs/log"
	tmos "github.com/tendermint/tendermint/libs/os"
	dbm "github.com/tendermint/tm-db"
)

const appName = "WasmApp"

// We pull these out so we can set them with LDFLAGS in the Makefile
var (
	CLIDir       = ".fetchcli"
	NodeDir      = ".fetchd"
	Bech32Prefix = sdk.Bech32MainPrefix

	// If EnabledSpecificProposals is "", and this is "true", then enable all x/wasm proposals.
	// If EnabledSpecificProposals is "", and this is not "true", then disable all x/wasm proposals.
	ProposalsEnabled = "false"
	// If set to non-empty string it must be comma-separated list of values that are all a subset
	// of "EnableAllProposals" (takes precedence over ProposalsEnabled)
	// https://github.com/CosmWasm/wasmd/blob/02a54d33ff2c064f3539ae12d75d027d9c665f05/x/wasm/internal/types/proposal.go#L28-L34
	EnableSpecificProposals = ""
)

// GetEnabledProposals parses the ProposalsEnabled / EnableSpecificProposals values to
// produce a list of enabled proposals to pass into wasmd app.
func GetEnabledProposals() []wasm.ProposalType {
	if EnableSpecificProposals == "" {
		if ProposalsEnabled == "true" {
			return wasm.EnableAllProposals
		}
		return wasm.DisableAllProposals
	}
	chunks := strings.Split(EnableSpecificProposals, ",")
	proposals, err := wasm.ConvertToProposals(chunks)
	if err != nil {
		panic(err)
	}
	return proposals
}

// These constants are derived from the above variables.
// These are the ones we will want to use in the code, based on
// any overrides above
var (
	// DefaultCLIHome default home directories for wasmcli
	DefaultCLIHome = os.ExpandEnv("$HOME/") + CLIDir
	// DefaultNodeHome default home directories for wasmd
	DefaultNodeHome = os.ExpandEnv("$HOME/") + NodeDir

	// Bech32PrefixAccAddr defines the Bech32 prefix of an account's address
	Bech32PrefixAccAddr = Bech32Prefix
	// Bech32PrefixAccPub defines the Bech32 prefix of an account's public key
	Bech32PrefixAccPub = Bech32Prefix + sdk.PrefixPublic
	// Bech32PrefixValAddr defines the Bech32 prefix of a validator's operator address
	Bech32PrefixValAddr = Bech32Prefix + sdk.PrefixValidator + sdk.PrefixOperator
	// Bech32PrefixValPub defines the Bech32 prefix of a validator's operator public key
	Bech32PrefixValPub = Bech32Prefix + sdk.PrefixValidator + sdk.PrefixOperator + sdk.PrefixPublic
	// Bech32PrefixConsAddr defines the Bech32 prefix of a consensus node address
	Bech32PrefixConsAddr = Bech32Prefix + sdk.PrefixValidator + sdk.PrefixConsensus
	// Bech32PrefixConsPub defines the Bech32 prefix of a consensus node public key
	Bech32PrefixConsPub = Bech32Prefix + sdk.PrefixValidator + sdk.PrefixConsensus + sdk.PrefixPublic
)

var (
	// ModuleBasics The module BasicManager is in charge of setting up basic,
	// non-dependant module elements, such as codec registration
	// and genesis verification.
	ModuleBasics = module.NewBasicManager(
		genutil.AppModuleBasic{},
		auth.AppModuleBasic{},
		bank.AppModuleBasic{},
		staking.AppModuleBasic{},
		mint.AppModuleBasic{},
		distr.AppModuleBasic{},
		gov.NewAppModuleBasic(append(wasmclient.ProposalHandlers, paramsclient.ProposalHandler, distr.ProposalHandler, upgradeclient.ProposalHandler)...),
		params.AppModuleBasic{},
		wasm.AppModuleBasic{},
		crisis.AppModuleBasic{},
		slashing.AppModuleBasic{},
		supply.AppModuleBasic{},
		upgrade.AppModuleBasic{},
		evidence.AppModuleBasic{},
	)

	// module account permissions
	maccPerms = map[string][]string{
		auth.FeeCollectorName:     nil,
		distr.ModuleName:          nil,
		mint.ModuleName:           {supply.Minter},
		staking.BondedPoolName:    {supply.Burner, supply.Staking},
		staking.NotBondedPoolName: {supply.Burner, supply.Staking},
		gov.ModuleName:            {supply.Burner},
	}
)

// MakeCodec creates the application codec. The codec is sealed before it is
// returned.
func MakeCodec() *codec.Codec {
	var cdc = codec.New()

	ModuleBasics.RegisterCodec(cdc)
	sdk.RegisterCodec(cdc)
	codec.RegisterCrypto(cdc)
	codec.RegisterEvidences(cdc)
	authvesting.RegisterCodec(cdc)

	return cdc.Seal()
}

// Verify app interface at compile time
var _ simapp.App = (*WasmApp)(nil)

// WasmApp extended ABCI application
type WasmApp struct {
	*bam.BaseApp
	cdc *codec.Codec

	invCheckPeriod uint

	// keys to access the substores
	keys  map[string]*sdk.KVStoreKey
	tKeys map[string]*sdk.TransientStoreKey

	// subspaces
	subspaces map[string]params.Subspace

	// keepers
	accountKeeper  auth.AccountKeeper
	bankKeeper     bank.Keeper
	supplyKeeper   supply.Keeper
	stakingKeeper  staking.Keeper
	slashingKeeper slashing.Keeper
	mintKeeper     mint.Keeper
	distrKeeper    distr.Keeper
	govKeeper      gov.Keeper
	crisisKeeper   crisis.Keeper
	paramsKeeper   params.Keeper
	evidenceKeeper *evidence.Keeper
	upgradeKeeper  upgrade.Keeper
	wasmKeeper     wasm.Keeper

	// the module manager
	mm *module.Manager

	// simulation manager
	sm *module.SimulationManager
}

// WasmWrapper allows us to use namespacing in the config file
// This is only used for parsing in the app, x/wasm expects WasmConfig
type WasmWrapper struct {
	Wasm wasm.WasmConfig `mapstructure:"wasm"`
}

// NewWasmApp returns a reference to an initialized WasmApp.
func NewWasmApp(logger log.Logger, db dbm.DB, traceStore io.Writer, loadLatest bool,
	invCheckPeriod uint, enabledProposals []wasm.ProposalType, skipUpgradeHeights map[int64]bool,
	baseAppOptions ...func(*bam.BaseApp)) *WasmApp {

	cdc := MakeCodec()

	debug := viper.GetBool(cli.TraceFlag)
	baseAppOptions = append(baseAppOptions, bam.SetTrace(debug))

	bApp := bam.NewBaseApp(appName, logger, db, auth.DefaultTxDecoder(cdc), baseAppOptions...)
	bApp.SetCommitMultiStoreTracer(traceStore)
	bApp.SetAppVersion(version.Version)

	keys := sdk.NewKVStoreKeys(
		bam.MainStoreKey, auth.StoreKey, staking.StoreKey,
		supply.StoreKey, mint.StoreKey, distr.StoreKey, slashing.StoreKey,
		gov.StoreKey, params.StoreKey, evidence.StoreKey, upgrade.StoreKey,
		wasm.StoreKey,
	)
	tKeys := sdk.NewTransientStoreKeys(staking.TStoreKey, params.TStoreKey)

	app := &WasmApp{
		BaseApp:        bApp,
		cdc:            cdc,
		invCheckPeriod: invCheckPeriod,
		keys:           keys,
		tKeys:          tKeys,
		subspaces:      make(map[string]params.Subspace),
	}

	// init params keeper and subspaces
	app.paramsKeeper = params.NewKeeper(app.cdc, keys[params.StoreKey], tKeys[params.TStoreKey])
	app.subspaces[auth.ModuleName] = app.paramsKeeper.Subspace(auth.DefaultParamspace)
	app.subspaces[bank.ModuleName] = app.paramsKeeper.Subspace(bank.DefaultParamspace)
	app.subspaces[staking.ModuleName] = app.paramsKeeper.Subspace(staking.DefaultParamspace)
	app.subspaces[mint.ModuleName] = app.paramsKeeper.Subspace(mint.DefaultParamspace)
	app.subspaces[distr.ModuleName] = app.paramsKeeper.Subspace(distr.DefaultParamspace)
	app.subspaces[slashing.ModuleName] = app.paramsKeeper.Subspace(slashing.DefaultParamspace)
	app.subspaces[gov.ModuleName] = app.paramsKeeper.Subspace(gov.DefaultParamspace).WithKeyTable(gov.ParamKeyTable())
	app.subspaces[crisis.ModuleName] = app.paramsKeeper.Subspace(crisis.DefaultParamspace)
	app.subspaces[evidence.ModuleName] = app.paramsKeeper.Subspace(evidence.DefaultParamspace)
	app.subspaces[wasm.ModuleName] = app.paramsKeeper.Subspace(wasm.DefaultParamspace)

	// add keepers
	app.accountKeeper = auth.NewAccountKeeper(
		app.cdc, keys[auth.StoreKey], app.subspaces[auth.ModuleName], auth.ProtoBaseAccount,
	)
	bankKeeper := bank.NewBaseKeeper(
		app.accountKeeper, app.subspaces[bank.ModuleName], app.ModuleAccountAddrs(),
	)
	app.supplyKeeper = supply.NewKeeper(
		app.cdc, keys[supply.StoreKey], app.accountKeeper, &bankKeeper, maccPerms,
	)
	stakingKeeper := staking.NewKeeper(
		app.cdc, keys[staking.StoreKey], app.supplyKeeper, app.subspaces[staking.ModuleName],
	)
	app.mintKeeper = mint.NewKeeper(
		app.cdc, keys[mint.StoreKey], app.subspaces[mint.ModuleName], &stakingKeeper,
		app.supplyKeeper, auth.FeeCollectorName,
	)
	app.distrKeeper = distr.NewKeeper(
		app.cdc, keys[distr.StoreKey], app.subspaces[distr.ModuleName], &stakingKeeper,
		app.supplyKeeper, auth.FeeCollectorName, app.ModuleAccountAddrs(),
	)
	app.slashingKeeper = slashing.NewKeeper(
		app.cdc, keys[slashing.StoreKey], &stakingKeeper, app.subspaces[slashing.ModuleName],
	)
	app.crisisKeeper = crisis.NewKeeper(
		app.subspaces[crisis.ModuleName], invCheckPeriod, app.supplyKeeper, auth.FeeCollectorName,
	)
	app.upgradeKeeper = upgrade.NewKeeper(skipUpgradeHeights, keys[upgrade.StoreKey], app.cdc)

	// create evidence keeper with evidence router
	evidenceKeeper := evidence.NewKeeper(
		app.cdc, keys[evidence.StoreKey], app.subspaces[evidence.ModuleName], &stakingKeeper, app.slashingKeeper,
	)
	evidenceRouter := evidence.NewRouter()

	// TODO: register evidence routes
	evidenceKeeper.SetRouter(evidenceRouter)

	app.evidenceKeeper = evidenceKeeper

	// register the proposal types
	govRouter := gov.NewRouter()
	govRouter.AddRoute(gov.RouterKey, gov.ProposalHandler).
		AddRoute(params.RouterKey, params.NewParamChangeProposalHandler(app.paramsKeeper)).
		AddRoute(distr.RouterKey, distr.NewCommunityPoolSpendProposalHandler(app.distrKeeper)).
		AddRoute(upgrade.RouterKey, upgrade.NewSoftwareUpgradeProposalHandler(app.upgradeKeeper))

	// register the staking hooks
	// NOTE: stakingKeeper above is passed by reference, so that it will contain these hooks
	app.stakingKeeper = *stakingKeeper.SetHooks(
		staking.NewMultiStakingHooks(app.distrKeeper.Hooks(), app.slashingKeeper.Hooks()),
	)

	// Set function for obtaining bond denomination in bank
	app.bankKeeper = bankKeeper.SetBondDenomFunc(app.stakingKeeper.BondDenom)

	// just re-use the full router - do we want to limit this more?
	var wasmRouter = bApp.Router()
	// better way to get this dir???
	homeDir := viper.GetString(cli.HomeFlag)
	fetchdir := filepath.Join(homeDir, "wasm")

	wasmWrap := WasmWrapper{Wasm: wasm.DefaultWasmConfig()}
	err := viper.Unmarshal(&wasmWrap)
	if err != nil {
		panic("error while reading wasm config: " + err.Error())
	}
	wasmConfig := wasmWrap.Wasm

	// The last arguments can contain custom message handlers, and custom query handlers,
	// if we want to allow any custom callbacks
	supportedFeatures := "staking"
	app.wasmKeeper = wasm.NewKeeper(app.cdc, keys[wasm.StoreKey], app.subspaces[wasm.ModuleName], app.accountKeeper, app.bankKeeper, app.stakingKeeper, wasmRouter, fetchdir, wasmConfig, supportedFeatures, nil, nil)

	// The gov proposal types can be individually enabled
	if len(enabledProposals) != 0 {
		govRouter.AddRoute(wasm.RouterKey, wasm.NewWasmProposalHandler(app.wasmKeeper, enabledProposals))
	}

	app.govKeeper = gov.NewKeeper(
		app.cdc, keys[gov.StoreKey], app.subspaces[gov.ModuleName],
		app.supplyKeeper, &stakingKeeper, govRouter,
	)

	// NOTE: Any module instantiated in the module manager that is later modified
	// must be passed by reference here.
	app.mm = module.NewManager(
		genutil.NewAppModule(app.accountKeeper, app.stakingKeeper, app.BaseApp.DeliverTx),
		auth.NewAppModule(app.accountKeeper),
		bank.NewAppModule(app.bankKeeper, app.accountKeeper),
		crisis.NewAppModule(&app.crisisKeeper),
		supply.NewAppModule(app.supplyKeeper, app.accountKeeper),
		gov.NewAppModule(app.govKeeper, app.accountKeeper, app.supplyKeeper),
		mint.NewAppModule(app.mintKeeper),
		slashing.NewAppModule(app.slashingKeeper, app.accountKeeper, app.stakingKeeper),
		distr.NewAppModule(app.distrKeeper, app.accountKeeper, app.supplyKeeper, app.stakingKeeper),
		staking.NewAppModule(app.stakingKeeper, app.accountKeeper, app.supplyKeeper),
		evidence.NewAppModule(*app.evidenceKeeper),
		wasm.NewAppModule(app.wasmKeeper),
		upgrade.NewAppModule(app.upgradeKeeper),
		evidence.NewAppModule(*app.evidenceKeeper),
	)
	// During begin block slashing happens after distr.BeginBlocker so that
	// there is nothing left over in the validator fee pool, so as to keep the
	// CanWithdrawInvariant invariant.

	app.mm.SetOrderBeginBlockers(upgrade.ModuleName, staking.ModuleName, mint.ModuleName, distr.ModuleName, slashing.ModuleName)
	app.mm.SetOrderEndBlockers(crisis.ModuleName, gov.ModuleName, staking.ModuleName)

	// NOTE: The genutils module must occur after staking so that pools are
	// properly initialized with tokens from genesis accounts.
	app.mm.SetOrderInitGenesis(
		distr.ModuleName, staking.ModuleName, auth.ModuleName, bank.ModuleName,
		slashing.ModuleName, gov.ModuleName, mint.ModuleName, supply.ModuleName,
		crisis.ModuleName, genutil.ModuleName, evidence.ModuleName, wasm.ModuleName,
	)

	app.mm.RegisterInvariants(&app.crisisKeeper)
	app.mm.RegisterRoutes(app.Router(), app.QueryRouter())

	// create the simulation manager and define the order of the modules for deterministic simulations
	//
	// NOTE: This is not required for apps that don't use the simulator for fuzz testing
	// transactions.
	app.sm = module.NewSimulationManager(
		auth.NewAppModule(app.accountKeeper),
		bank.NewAppModule(app.bankKeeper, app.accountKeeper),
		supply.NewAppModule(app.supplyKeeper, app.accountKeeper),
		gov.NewAppModule(app.govKeeper, app.accountKeeper, app.supplyKeeper),
		mint.NewAppModule(app.mintKeeper),
		distr.NewAppModule(app.distrKeeper, app.accountKeeper, app.supplyKeeper, app.stakingKeeper),
		staking.NewAppModule(app.stakingKeeper, app.accountKeeper, app.supplyKeeper),
		slashing.NewAppModule(app.slashingKeeper, app.accountKeeper, app.stakingKeeper),
	)

	app.sm.RegisterStoreDecoders()

	// initialize stores
	app.MountKVStores(keys)
	app.MountTransientStores(tKeys)

	// initialize BaseApp
	app.SetInitChainer(app.InitChainer)
	app.SetBeginBlocker(app.BeginBlocker)
	app.SetAnteHandler(auth.NewAnteHandler(app.accountKeeper, app.supplyKeeper, auth.DefaultSigVerificationGasConsumer))
	app.SetEndBlocker(app.EndBlocker)

	if loadLatest {
		err := app.LoadLatestVersion(app.keys[bam.MainStoreKey])
		if err != nil {
			tmos.Exit(err.Error())
		}
	}

	return app
}

// Name returns the name of the App
func (app *WasmApp) Name() string { return app.BaseApp.Name() }

// application updates every begin block
func (app *WasmApp) BeginBlocker(ctx sdk.Context, req abci.RequestBeginBlock) abci.ResponseBeginBlock {
	return app.mm.BeginBlock(ctx, req)
}

// EndBlocker application updates every end block
func (app *WasmApp) EndBlocker(ctx sdk.Context, req abci.RequestEndBlock) abci.ResponseEndBlock {
	return app.mm.EndBlock(ctx, req)
}

// InitChainer application update at chain initialization
func (app *WasmApp) InitChainer(ctx sdk.Context, req abci.RequestInitChain) abci.ResponseInitChain {
	var genesisState simapp.GenesisState
	app.cdc.MustUnmarshalJSON(req.AppStateBytes, &genesisState)

	return app.mm.InitGenesis(ctx, genesisState)
}

// LoadHeight loads a particular height
func (app *WasmApp) LoadHeight(height int64) error {
	return app.LoadVersion(height, app.keys[bam.MainStoreKey])
}

// ModuleAccountAddrs returns all the app's module account addresses.
func (app *WasmApp) ModuleAccountAddrs() map[string]bool {
	modAccAddrs := make(map[string]bool)
	for acc := range maccPerms {
		modAccAddrs[supply.NewModuleAddress(acc).String()] = true
	}

	return modAccAddrs
}

// Codec returns the application's sealed codec.
func (app *WasmApp) Codec() *codec.Codec {
	return app.cdc
}

// SimulationManager implements the SimulationApp interface
func (app *WasmApp) SimulationManager() *module.SimulationManager {
	return app.sm
}

// GetMaccPerms returns a mapping of the application's module account permissions.
func GetMaccPerms() map[string][]string {
	modAccPerms := make(map[string][]string)
	for k, v := range maccPerms {
		modAccPerms[k] = v
	}
	return modAccPerms
}

func (app *WasmApp) RegisterAPIRoutes(server *api.Server) {
	client.RegisterRoutes(server.ClientCtx, server.Router)
	authrest.RegisterTxRoutes(server.ClientCtx, server.Router)
	ModuleBasics.RegisterRESTRoutes(server.ClientCtx, server.Router)
}
