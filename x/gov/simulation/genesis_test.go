package simulation_test

import (
	"encoding/json"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
	"gotest.tools/v3/assert"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	simtypes "github.com/cosmos/cosmos-sdk/types/simulation"
	"github.com/cosmos/cosmos-sdk/x/gov/simulation"
	"github.com/cosmos/cosmos-sdk/x/gov/types"
	v1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
)

// TestRandomizedGenState tests the normal scenario of applying RandomizedGenState.
// Abnormal scenarios are not tested here.
func TestRandomizedGenState(t *testing.T) {
	interfaceRegistry := codectypes.NewInterfaceRegistry()
	cdc := codec.NewProtoCodec(interfaceRegistry)

	s := rand.NewSource(1)
	r := rand.New(s)

	simState := module.SimulationState{
		AppParams:    make(simtypes.AppParams),
		Cdc:          cdc,
		Rand:         r,
		NumBonded:    3,
		BondDenom:    sdk.DefaultBondDenom,
		Accounts:     simtypes.RandomAccounts(r, 3),
		InitialStake: sdkmath.NewInt(1000),
		GenState:     make(map[string]json.RawMessage),
	}

	simulation.RandomizedGenState(&simState)

	var govGenesis v1.GenesisState
	simState.Cdc.MustUnmarshalJSON(simState.GenState[types.ModuleName], &govGenesis)

	const (
		tallyQuorum             = "0.466000000000000000"
		tallyThreshold          = "0.485000000000000000"
		tallyExpeditedThreshold = "0.511000000000000000"
		tallyVetoThreshold      = "0.291000000000000000"
		minInitialDepositDec    = "0.880000000000000000"
	)

	assert.Equal(t, "272stake", govGenesis.Params.MinDeposit[0].String())
	assert.Equal(t, "800stake", govGenesis.Params.ExpeditedMinDeposit[0].String())
	assert.Equal(t, "41h11m36s", govGenesis.Params.MaxDepositPeriod.String())
	assert.Equal(t, float64(307362), govGenesis.Params.VotingPeriod.Seconds())
	assert.Equal(t, float64(115820), govGenesis.Params.ExpeditedVotingPeriod.Seconds())
	require.Equal(t, tallyQuorum, govGenesis.Params.Quorum)
	require.Equal(t, tallyThreshold, govGenesis.Params.Threshold)
	assert.Equal(t, tallyExpeditedThreshold, govGenesis.Params.ExpeditedThreshold)
	require.Equal(t, tallyVetoThreshold, govGenesis.Params.VetoThreshold)
	require.Equal(t, minInitialDepositDec, govGenesis.Params.MinInitialDepositRatio)
	require.Equal(t, uint64(0x28), govGenesis.StartingProposalId)
	require.Equal(t, []*v1.Deposit{}, govGenesis.Deposits)
	require.Equal(t, []*v1.Vote{}, govGenesis.Votes)
	require.Equal(t, []*v1.Proposal{}, govGenesis.Proposals)
}

// TestRandomizedGenState tests abnormal scenarios of applying RandomizedGenState.
func TestRandomizedGenState1(t *testing.T) {
	interfaceRegistry := codectypes.NewInterfaceRegistry()
	cdc := codec.NewProtoCodec(interfaceRegistry)

	s := rand.NewSource(1)
	r := rand.New(s)
	// all these tests will panic
	tests := []struct {
		simState module.SimulationState
		panicMsg string
	}{
		{ // panic => reason: incomplete initialization of the simState
			module.SimulationState{}, "invalid memory address or nil pointer dereference"},
		{ // panic => reason: incomplete initialization of the simState
			module.SimulationState{
				AppParams: make(simtypes.AppParams),
				Cdc:       cdc,
				Rand:      r,
			}, "assignment to entry in nil map"},
	}

	for _, tt := range tests {
		require.Panicsf(t, func() { simulation.RandomizedGenState(&tt.simState) }, tt.panicMsg)
	}
}
