package ibftswitch

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/consensus/ibft/fork"
	"github.com/xgr-network/xgr-node/consensus/ibft/signer"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

func TestAppendIBFTForks_PoSCarriesLastPoAValidatorsAsBootstrap(t *testing.T) {
	t.Parallel()

	poaValidators := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000001")),
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000002")),
	)

	cc := &chain.Chain{
		Genesis: &chain.Genesis{
			ExtraData: mustGenesisExtraWithValidators(t, poaValidators),
		},
		Params: &chain.Params{
			Engine: map[string]interface{}{
				"ibft": map[string]interface{}{
					fork.KeyTypes: []interface{}{
						map[string]interface{}{
							"type":           "PoA",
							"validator_type": "ecdsa",
							"from":           0,
							"validators": []interface{}{
								map[string]interface{}{"Address": "0x1000000000000000000000000000000000000001"},
								map[string]interface{}{"Address": "0x1000000000000000000000000000000000000002"},
							},
						},
					},
				},
			},
		},
	}

	require.NoError(t, appendIBFTForks(
		cc,
		fork.PoS,
		validators.ECDSAValidatorType,
		11,
		u64ptr(10),
		nil,
		u64ptr(20),
		u64ptr(2),
	))

	ibftConfig := cc.Params.Engine["ibft"].(map[string]interface{})
	ibftForks := ibftConfig[fork.KeyTypes].(fork.IBFTForks)
	require.Len(t, ibftForks, 2)
	require.Equal(t, poaValidators.Len(), ibftForks[1].Validators.Len())
	require.Equal(t, poaValidators.At(0).Addr(), ibftForks[1].Validators.At(0).Addr())
}

func TestAppendIBFTForks_PoSFailsWhenLastForkValidatorTypeMismatches(t *testing.T) {
	t.Parallel()

	poaValidators := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(types.StringToAddress("0x1100000000000000000000000000000000000001")),
		validators.NewECDSAValidator(types.StringToAddress("0x1100000000000000000000000000000000000002")),
	)

	cc := &chain.Chain{
		Genesis: &chain.Genesis{
			ExtraData: mustGenesisExtraWithValidators(t, poaValidators),
		},
		Params: &chain.Params{
			Engine: map[string]interface{}{
				"ibft": map[string]interface{}{
					fork.KeyTypes: []interface{}{
						map[string]interface{}{
							"type":           "PoA",
							"validator_type": "ecdsa",
							"from":           0,
							"validators": []interface{}{
								map[string]interface{}{"Address": "0x1100000000000000000000000000000000000001"},
								map[string]interface{}{"Address": "0x1100000000000000000000000000000000000002"},
							},
						},
					},
				},
			},
		},
	}

	err := appendIBFTForks(
		cc,
		fork.PoS,
		validators.BLSValidatorType,
		11,
		u64ptr(10),
		nil,
		u64ptr(20),
		u64ptr(2),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "expected validator type bls")
	require.Contains(t, err.Error(), "source last-fork")
	require.Contains(t, err.Error(), "type mismatch")
}

func TestAppendIBFTForks_PoSFallsBackToGenesisExtraValidators(t *testing.T) {
	t.Parallel()

	genesisValidators := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(types.StringToAddress("0x2000000000000000000000000000000000000001")),
		validators.NewECDSAValidator(types.StringToAddress("0x2000000000000000000000000000000000000002")),
	)

	cc := &chain.Chain{
		Genesis: &chain.Genesis{
			ExtraData: mustGenesisExtraWithValidators(t, genesisValidators),
		},
		Params: &chain.Params{
			Engine: map[string]interface{}{
				"ibft": map[string]interface{}{
					fork.KeyTypes: []interface{}{
						map[string]interface{}{
							"type":           "PoA",
							"validator_type": "ecdsa",
							"from":           0,
						},
					},
				},
			},
		},
	}

	require.NoError(t, appendIBFTForks(
		cc,
		fork.PoS,
		validators.ECDSAValidatorType,
		5,
		u64ptr(4),
		nil,
		u64ptr(10),
		u64ptr(1),
	))

	ibftConfig := cc.Params.Engine["ibft"].(map[string]interface{})
	ibftForks := ibftConfig[fork.KeyTypes].(fork.IBFTForks)
	require.Len(t, ibftForks, 2)
	require.Equal(t, genesisValidators.Len(), ibftForks[1].Validators.Len())
	require.Equal(t, genesisValidators.At(1).Addr(), ibftForks[1].Validators.At(1).Addr())
}

func TestAppendIBFTForks_PoSWithCLIBLSValidators(t *testing.T) {
	t.Parallel()

	cliValidators := validators.NewBLSValidatorSet(
		validators.NewBLSValidator(
			types.StringToAddress("0x3000000000000000000000000000000000000001"),
			[]byte{1, 2, 3},
		),
		validators.NewBLSValidator(
			types.StringToAddress("0x3000000000000000000000000000000000000002"),
			[]byte{4, 5, 6},
		),
	)

	cc := &chain.Chain{
		Genesis: &chain.Genesis{
			ExtraData: mustGenesisExtraWithValidators(t, validators.NewECDSAValidatorSet()),
		},
		Params: &chain.Params{
			Engine: map[string]interface{}{
				"ibft": map[string]interface{}{
					fork.KeyTypes: []interface{}{
						map[string]interface{}{
							"type":           "PoA",
							"validator_type": "ecdsa",
							"from":           0,
						},
					},
				},
			},
		},
	}

	require.NoError(t, appendIBFTForks(
		cc,
		fork.PoS,
		validators.BLSValidatorType,
		15,
		u64ptr(14),
		cliValidators,
		u64ptr(50),
		u64ptr(1),
	))

	ibftConfig := cc.Params.Engine["ibft"].(map[string]interface{})
	ibftForks := ibftConfig[fork.KeyTypes].(fork.IBFTForks)
	require.Len(t, ibftForks, 2)
	require.Equal(t, validators.BLSValidatorType, ibftForks[1].Validators.Type())
	require.Equal(t, cliValidators.Len(), ibftForks[1].Validators.Len())
}

func TestAppendIBFTForks_PoSFailsWhenGenesisExtraValidatorTypeMismatches(t *testing.T) {
	t.Parallel()

	genesisECDSAValidators := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(types.StringToAddress("0x4000000000000000000000000000000000000001")),
		validators.NewECDSAValidator(types.StringToAddress("0x4000000000000000000000000000000000000002")),
	)

	cc := &chain.Chain{
		Genesis: &chain.Genesis{
			ExtraData: mustGenesisExtraWithValidators(t, genesisECDSAValidators),
		},
		Params: &chain.Params{
			Engine: map[string]interface{}{
				"ibft": map[string]interface{}{
					fork.KeyTypes: []interface{}{
						map[string]interface{}{
							"type":           "PoA",
							"validator_type": "ecdsa",
							"from":           0,
						},
					},
				},
			},
		},
	}

	err := appendIBFTForks(
		cc,
		fork.PoS,
		validators.BLSValidatorType,
		7,
		u64ptr(6),
		nil,
		u64ptr(20),
		u64ptr(1),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "expected validator type bls")
	require.Contains(t, err.Error(), "source genesis-extra")
	require.Contains(t, err.Error(), "type mismatch")
}

func TestAppendIBFTForks_PoSRequiresBootstrapValidators(t *testing.T) {
	t.Parallel()

	cc := &chain.Chain{
		Genesis: &chain.Genesis{
			ExtraData: make([]byte, signer.IstanbulExtraVanity),
		},
		Params: &chain.Params{
			Engine: map[string]interface{}{
				"ibft": map[string]interface{}{
					fork.KeyTypes: []interface{}{
						map[string]interface{}{
							"type":           "PoA",
							"validator_type": "ecdsa",
							"from":           0,
						},
					},
				},
			},
		},
	}

	err := appendIBFTForks(
		cc,
		fork.PoS,
		validators.ECDSAValidatorType,
		5,
		u64ptr(4),
		nil,
		u64ptr(10),
		u64ptr(1),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unable to determine PoS bootstrap validators")
}

func mustGenesisExtraWithValidators(t *testing.T, vals validators.Validators) []byte {
	t.Helper()

	extra := &signer.IstanbulExtra{
		Validators:     vals,
		ProposerSeal:   []byte{},
		CommittedSeals: &signer.SerializedSeal{},
	}

	out := make([]byte, signer.IstanbulExtraVanity)
	return extra.MarshalRLPTo(out)
}

func u64ptr(v uint64) *uint64 {
	return &v
}
