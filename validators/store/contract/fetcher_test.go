package contract

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	testHelper "github.com/xgr-network/xgr-node/helper/tests"
	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

func TestFetchValidators(t *testing.T) {
	t.Parallel()

	// only check error handling because of the duplicated tests below
	fakeValidatorType := validators.ValidatorType("fake")
	res, err := FetchValidators(
		fakeValidatorType,
		nil,
		types.ZeroAddress,
	)

	assert.Nil(t, res)
	assert.ErrorContains(t, err, fmt.Sprintf("unsupported validator type: %s", fakeValidatorType))
}

func TestFetchECDSAValidators(t *testing.T) {
	t.Parallel()

	var (
		ecdsaValidators = validators.NewECDSAValidatorSet(
			validators.NewECDSAValidator(addr1),
			validators.NewECDSAValidator(addr2),
		)
	)

	tests := []struct {
		name        string
		transition  *state.Transition
		from        types.Address
		expectedRes validators.Validators
		expectedErr error
	}{
		{
			name: "should return error if QueryValidators failed",
			transition: newTestTransition(
				t,
			),
			from:        types.ZeroAddress,
			expectedRes: nil,
			expectedErr: errors.New("empty input"),
		},
		{
			name: "should return ECDSA Validators",
			transition: newTestTransitionWithPredeployedStakingContract(
				t,
				ecdsaValidators,
			),
			from:        types.ZeroAddress,
			expectedRes: ecdsaValidators,
			expectedErr: nil,
		},
	}

	for _, test := range tests {
		test := test

		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			res, err := FetchValidators(
				validators.ECDSAValidatorType,
				test.transition,
				test.from,
			)

			assert.Equal(t, test.expectedRes, res)
			testHelper.AssertErrorMessageContains(t, test.expectedErr, err)
		})
	}
}

func TestFetchBLSValidators(t *testing.T) {
	t.Parallel()

	var (
		blsValidators = validators.NewBLSValidatorSet(
			validators.NewBLSValidator(addr1, testBLSPubKey1),
			validators.NewBLSValidator(addr2, []byte{}), // validator 2 has not set BLS Public Key
		)
	)

	tests := []struct {
		name        string
		transition  *state.Transition
		from        types.Address
		expectedRes validators.Validators
		expectedErr error
	}{
		{
			name: "should return error if QueryValidators failed",
			transition: newTestTransition(
				t,
			),
			from:        types.ZeroAddress,
			expectedRes: nil,
			expectedErr: errors.New("empty input"),
		},
		{
			name: "should return ECDSA Validators",
			transition: newTestTransitionWithPredeployedStakingContract(
				t,
				blsValidators,
			),
			from: types.ZeroAddress,
			expectedRes: validators.NewBLSValidatorSet(
				validators.NewBLSValidator(addr1, testBLSPubKey1),
			),
			expectedErr: nil,
		},
	}

	for _, test := range tests {
		test := test

		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			res, err := FetchValidators(
				validators.BLSValidatorType,
				test.transition,
				test.from,
			)

			assert.Equal(t, test.expectedRes, res)
			testHelper.AssertErrorMessageContains(t, test.expectedErr, err)
		})
	}
}
