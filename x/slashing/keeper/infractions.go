package keeper

import (
	"fmt"

	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/slashing/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// HandleValidatorSignature handles a validator signature, must be called once per validator per block.
func (k Keeper) HandleValidatorSignature(ctx sdk.Context, addr cryptotypes.Address, power int64, signed bool) {
	params := k.GetParams(ctx)
	k.HandleValidatorSignatureWithParams(ctx, params, addr, power, signed)
}

func (k Keeper) HandleValidatorSignatureWithParams(ctx sdk.Context, params types.Params, addr cryptotypes.Address, power int64, signed bool) {
	logger := k.Logger(ctx)
	height := ctx.BlockHeight()

	// fetch the validator public key
	consAddr := sdk.ConsAddress(addr)

	// don't update missed blocks when validator's jailed
	if k.sk.IsValidatorJailed(ctx, consAddr) {
		return
	}

	// fetch signing info
	signInfo, found := k.GetValidatorSigningInfo(ctx, consAddr)
	if !found {
		panic(fmt.Sprintf("Expected signing info for validator %s but not found", consAddr))
	}

	signedBlocksWindow := params.SignedBlocksWindow

	// Compute the relative index, so we count the blocks the validator *should*
	// have signed. We will also use the 0-value default signing info if not present.
	// The index is in the range [0, SignedBlocksWindow)
	// and is used to see if a validator signed a block at the given height, which
	// is represented by a bit in the bitmap.
	// The validator start height should get mapped to index 0, so we computed index as:
	// (height - startHeight) % signedBlocksWindow
	//
	// NOTE: There is subtle different behavior between genesis validators and non-genesis validators.
	// A genesis validator will start at index 0, whereas a non-genesis validator's startHeight will be the block
	// they bonded on, but the first block they vote on will be one later. (And thus their first vote is at index 1)
	index := (height - signInfo.StartHeight) % signedBlocksWindow
	if signInfo.StartHeight > height {
		panic(fmt.Errorf("invalid state, the validator %v has start height %d , which is greater than the current height %d (as parsed from the header)",
			signInfo.Address, signInfo.StartHeight, height))
	}
	// Update signed block bit array & counter
	// This counter just tracks the sum of the bit array
	// That way we avoid needing to read/write the whole array each time
	previous, err := k.GetMissedBlockBitmapValue(ctx, consAddr, index)
	if err != nil {
		panic(fmt.Sprintf("Expected to get missed block bitmap value for validator %s at index %d but not found, error: %v", consAddr, index, err))
	}
	modifiedSignInfo := false
	missed := !signed
	switch {
	case !previous && missed:
		modifiedSignInfo = true
		// Bitmap value has changed from not missed to missed, so we flip the bit
		// and increment the counter.
		if err := k.SetMissedBlockBitmapValue(ctx, consAddr, index, true); err != nil {
			panic(fmt.Sprintf("Expected to set missed block bitmap value for validator %s at index %d but not found, error: %v", consAddr, index, err))
		}

		signInfo.MissedBlocksCounter++

	case previous && !missed:
		modifiedSignInfo = true
		// Bitmap value has changed from missed to not missed, so we flip the bit
		// and decrement the counter.
		if err := k.SetMissedBlockBitmapValue(ctx, consAddr, index, false); err != nil {
			panic(fmt.Sprintf("Expected to set missed block bitmap value for validator %s at index %d but not found, error: %v", consAddr, index, err))
		}

		signInfo.MissedBlocksCounter--

	default:
		// Array value at this index has not changed, no need to update counter
	}

	minSignedPerWindow := params.MinSignedPerWindowInt()

	if missed {
		ctx.EventManager().EmitEvent(
			sdk.NewEvent(
				types.EventTypeLiveness,
				sdk.NewAttribute(types.AttributeKeyAddress, consAddr.String()),
				sdk.NewAttribute(types.AttributeKeyMissedBlocks, fmt.Sprintf("%d", signInfo.MissedBlocksCounter)),
				sdk.NewAttribute(types.AttributeKeyHeight, fmt.Sprintf("%d", height)),
			),
		)

		logger.Debug(
			"absent validator",
			"height", height,
			"validator", consAddr.String(),
			"missed", signInfo.MissedBlocksCounter,
			"threshold", minSignedPerWindow,
		)
	}

	minHeight := signInfo.StartHeight + params.SignedBlocksWindow
	maxMissed := params.SignedBlocksWindow - minSignedPerWindow

	// if we are past the minimum height and the validator has missed too many blocks, punish them
	if height > minHeight && signInfo.MissedBlocksCounter > maxMissed {
		modifiedSignInfo = true
		validator := k.sk.ValidatorByConsAddr(ctx, consAddr)
		if validator != nil && !validator.IsJailed() {
			// Downtime confirmed: slash and jail the validator
			// We need to retrieve the stake distribution which signed the block, so we subtract ValidatorUpdateDelay from the evidence height,
			// and subtract an additional 1 since this is the LastCommit.
			// Note that this *can* result in a negative "distributionHeight" up to -ValidatorUpdateDelay-1,
			// i.e. at the end of the pre-genesis block (none) = at the beginning of the genesis block.
			// That's fine since this is just used to filter unbonding delegations & redelegations.
			distributionHeight := height - sdk.ValidatorUpdateDelay - 1

			coinsBurned := k.sk.SlashWithInfractionReason(ctx, consAddr, distributionHeight, power, k.SlashFractionDowntime(ctx), stakingtypes.Infraction_INFRACTION_DOWNTIME)
			ctx.EventManager().EmitEvent(
				sdk.NewEvent(
					types.EventTypeSlash,
					sdk.NewAttribute(types.AttributeKeyAddress, consAddr.String()),
					sdk.NewAttribute(types.AttributeKeyPower, fmt.Sprintf("%d", power)),
					sdk.NewAttribute(types.AttributeKeyReason, types.AttributeValueMissingSignature),
					sdk.NewAttribute(types.AttributeKeyJailed, consAddr.String()),
					sdk.NewAttribute(types.AttributeKeyBurnedCoins, coinsBurned.String()),
				),
			)
			k.sk.Jail(ctx, consAddr)

			signInfo.JailedUntil = ctx.BlockHeader().Time.Add(k.DowntimeJailDuration(ctx))

			// We need to reset the counter & array so that the validator won't be immediately slashed for downtime upon rebonding.
			// We don't set the start height as this will get correctly set
			// once they bond again in the AfterValidatorBonded hook!
			signInfo.MissedBlocksCounter = 0
			err = k.DeleteMissedBlockBitmap(ctx, consAddr)
			if err != nil {
				panic(fmt.Sprintf("Expected to delete missed block bitmap for validator %s but not found, error: %v", consAddr, err))
			}

			logger.Info(
				"slashing and jailing validator due to liveness fault",
				"height", height,
				"validator", consAddr.String(),
				"min_height", minHeight,
				"threshold", minSignedPerWindow,
				"slashed", k.SlashFractionDowntime(ctx).String(),
				"jailed_until", signInfo.JailedUntil,
			)
		} else {
			// validator was (a) not found or (b) already jailed so we do not slash
			logger.Info(
				"validator would have been slashed for downtime, but was either not found in store or already jailed",
				"validator", consAddr.String(),
			)
		}
	}

	// Set the updated signing info
	if modifiedSignInfo {
		k.SetValidatorSigningInfo(ctx, consAddr, signInfo)
	}
}
