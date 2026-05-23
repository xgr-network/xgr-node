package fork

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/consensus/ibft/pos"
	"github.com/xgr-network/xgr-node/consensus/ibft/signer"
	testHelper "github.com/xgr-network/xgr-node/helper/tests"
	"github.com/xgr-network/xgr-node/state"
	itrie "github.com/xgr-network/xgr-node/state/immutable-trie"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
	"github.com/xgr-network/xgr-node/validators/store"
	"github.com/xgr-network/xgr-node/validators/store/snapshot"
)

var (
	errTest = errors.New("test")
)

// a mock returning an error in UnmarshalJSON
type fakeUnmarshalerStruct struct{}

func (s *fakeUnmarshalerStruct) UnmarshalJSON(data []byte) error {
	return errTest
}

type mockSigner struct {
	signer.Signer

	TypeFn                func() validators.ValidatorType
	EcrecoverFromHeaderFn func(*types.Header) (types.Address, error)
	GetValidatorsFn       func(*types.Header) (validators.Validators, error)
}

func (m *mockSigner) Type() validators.ValidatorType {
	return m.TypeFn()
}

func (m *mockSigner) EcrecoverFromHeader(h *types.Header) (types.Address, error) {
	return m.EcrecoverFromHeaderFn(h)
}

func (m *mockSigner) GetValidators(h *types.Header) (validators.Validators, error) {
	return m.GetValidatorsFn(h)
}

func macroTestTransition(t *testing.T) *state.Transition {
	t.Helper()
	st := itrie.NewState(itrie.NewMemoryStorage())
	ex := state.NewExecutor(&chain.Params{Forks: chain.AllForksEnabled, BurnContract: map[uint64]types.Address{0: types.ZeroAddress}}, st, hclog.NewNullLogger())
	root, err := ex.WriteGenesis(nil, types.Hash{})
	require.NoError(t, err)
	ex.GetHash = func(*types.Header) state.GetHashByNumber { return func(uint64) types.Hash { return root } }
	tx, err := ex.BeginTxn(root, &types.Header{Number: 1, StateRoot: root}, types.ZeroAddress)
	require.NoError(t, err)
	return tx
}

func Test_isJSONSyntaxError(t *testing.T) {
	t.Parallel()

	var (
		// create some special errors
		snaps   = []*snapshot.Snapshot{}
		fakeStr = &fakeUnmarshalerStruct{}

		invalidJSONErr      = json.Unmarshal([]byte("foo"), &snaps)
		invalidUnmarshalErr = json.Unmarshal([]byte("{}"), fakeStr)
	)

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "should return false for nil",
			err:      nil,
			expected: false,
		},
		{
			name:     "should return false for custom error",
			err:      errTest,
			expected: false,
		},
		{
			name:     "should return marshal for json.InvalidUnmarshalError",
			err:      invalidUnmarshalErr,
			expected: false,
		},
		{
			name:     "should return json.SyntaxError",
			err:      invalidJSONErr,
			expected: true,
		},
	}

	for _, test := range tests {
		test := test

		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(
				t,
				test.expected,
				isJSONSyntaxError(test.err),
			)
		})
	}
}

func createTestMetadataJSON(height uint64) string {
	return fmt.Sprintf(`{"LastBlock": %d}`, height)
}

func createTestSnapshotJSON(t *testing.T, snapshot *snapshot.Snapshot) string {
	t.Helper()

	res, err := json.Marshal(snapshot)
	assert.NoError(t, err)

	return string(res)
}

func TestSnapshotValidatorStoreWrapper(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                   string
		storedSnapshotMetadata string
		storedSnapshots        string
		blockchain             store.HeaderGetter
		signer                 signer.Signer
		epochSize              uint64
		err                    error
	}{
		{
			name:                   "should return error if initialize fails",
			storedSnapshotMetadata: createTestMetadataJSON(0),
			storedSnapshots:        "[]",
			blockchain: &store.MockBlockchain{
				HeaderFn: func() *types.Header {
					return &types.Header{Number: 0}
				},
			},
			signer:    nil,
			epochSize: 10,
			err:       fmt.Errorf("signer not found %d", 0),
		},
		{
			name:                   "should succeed",
			storedSnapshotMetadata: createTestMetadataJSON(10),
			storedSnapshots: fmt.Sprintf("[%s]", createTestSnapshotJSON(
				t,
				&snapshot.Snapshot{
					Number: 10,
					Hash:   types.BytesToHash([]byte{0x10}).String(),
					Set:    validators.NewECDSAValidatorSet(),
					Votes:  []*store.Vote{},
				},
			)),
			blockchain: &store.MockBlockchain{
				HeaderFn: func() *types.Header {
					return &types.Header{Number: 10}
				},
			},
			signer:    nil,
			epochSize: 10,
			err:       nil,
		},
		// the below cases recover snapshots from local chain,
		// but this test just makes sure constructor doesn't return an error
		// because snapshot package has tests covering such cases
		{
			name:                   "should succeed and recover snapshots from headers when the files don't exist",
			storedSnapshotMetadata: "",
			storedSnapshots:        "",
			blockchain: &store.MockBlockchain{
				HeaderFn: func() *types.Header {
					return &types.Header{Number: 0}
				},
			},
			signer: &mockSigner{
				GetValidatorsFn: func(h *types.Header) (validators.Validators, error) {
					// height of the header HeaderFn returns
					assert.Equal(t, uint64(0), h.Number)

					return &validators.Set{}, nil
				},
			},
			epochSize: 10,
			err:       nil,
		},
		{
			name:                   "should succeed and recover snapshots from headers when the metadata file is broken",
			storedSnapshotMetadata: "broken data",
			storedSnapshots: fmt.Sprintf("[%s]", createTestSnapshotJSON(
				t,
				&snapshot.Snapshot{
					Number: 10,
					Hash:   types.BytesToHash([]byte{0x10}).String(),
					Set:    validators.NewECDSAValidatorSet(),
					Votes:  []*store.Vote{},
				},
			)),
			blockchain: &store.MockBlockchain{
				HeaderFn: func() *types.Header {
					return &types.Header{Number: 0}
				},
			},
			signer: &mockSigner{
				GetValidatorsFn: func(h *types.Header) (validators.Validators, error) {
					// height of the header HeaderFn returns
					assert.Equal(t, uint64(0), h.Number)

					return &validators.Set{}, nil
				},
			},
			epochSize: 10,
			err:       nil,
		},
		{
			name:                   "should succeed and recover snapshots from headers when the snapshots file is broken",
			storedSnapshotMetadata: createTestMetadataJSON(0),
			storedSnapshots:        "broken",
			blockchain: &store.MockBlockchain{
				HeaderFn: func() *types.Header {
					return &types.Header{Number: 0}
				},
			},
			signer: &mockSigner{
				GetValidatorsFn: func(h *types.Header) (validators.Validators, error) {
					// height of the header HeaderFn returns
					assert.Equal(t, uint64(0), h.Number)

					return &validators.Set{}, nil
				},
			},
			epochSize: 10,
			err:       nil,
		},
	}

	for _, test := range tests {
		test := test

		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dirPath := createTestTempDirectory(t)

			if len(test.storedSnapshotMetadata) != 0 {
				assert.NoError(
					t,
					os.WriteFile(path.Join(dirPath, snapshotMetadataFilename), []byte(test.storedSnapshotMetadata), 0775),
				)
			}

			if len(test.storedSnapshots) != 0 {
				assert.NoError(
					t,
					os.WriteFile(path.Join(dirPath, snapshotSnapshotsFilename), []byte(test.storedSnapshots), 0775),
				)
			}

			store, err := NewSnapshotValidatorStoreWrapper(
				hclog.NewNullLogger(),
				test.blockchain,
				func(u uint64) (signer.Signer, error) {
					return test.signer, nil
				},
				dirPath,
				test.epochSize,
			)

			testHelper.AssertErrorMessageContains(
				t,
				test.err,
				err,
			)

			if store != nil {
				assert.Equal(
					t,
					dirPath,
					store.dirPath,
				)
			}
		})
	}
}

func TestSnapshotValidatorStoreWrapperGetValidators(t *testing.T) {
	t.Parallel()

	var (
		epochSize uint64 = 10
		metadata         = &snapshot.SnapshotMetadata{
			LastBlock: 10,
		}
		snapshots = []*snapshot.Snapshot{
			{
				Number: 10,
				Hash:   types.StringToHash("1").String(),
				Set: validators.NewECDSAValidatorSet(
					validators.NewECDSAValidator(types.StringToAddress("1")),
				),
				Votes: []*store.Vote{},
			},
		}
	)

	snapshotStore, err := snapshot.NewSnapshotValidatorStore(
		hclog.NewNullLogger(),
		&store.MockBlockchain{
			HeaderFn: func() *types.Header {
				return &types.Header{Number: 10}
			},
		},
		func(u uint64) (snapshot.SignerInterface, error) {
			return nil, nil
		},
		epochSize,
		metadata,
		snapshots,
	)

	assert.NoError(t, err)

	wrapper := SnapshotValidatorStoreWrapper{
		SnapshotValidatorStore: snapshotStore,
	}

	vals, err := wrapper.GetValidators(11, 0, 0)
	assert.NoError(t, err)
	assert.Equal(t, snapshots[0].Set, vals)

	t.Run("should return error for genesis height", func(t *testing.T) {
		t.Parallel()

		genesisVals, err := wrapper.GetValidators(0, 0, 0)
		assert.Nil(t, genesisVals)
		assert.EqualError(t, err, "height must be greater than 0")
	})
}

func TestSnapshotValidatorStoreWrapperClose(t *testing.T) {
	t.Parallel()

	var (
		dirPath = createTestTempDirectory(t)

		epochSize uint64 = 10
		metadata         = &snapshot.SnapshotMetadata{
			LastBlock: 10,
		}
		snapshots = []*snapshot.Snapshot{
			{
				Number: 10,
				Hash:   types.StringToHash("1").String(),
				Set: validators.NewECDSAValidatorSet(
					validators.NewECDSAValidator(types.StringToAddress("1")),
				),
				Votes: []*store.Vote{},
			},
		}
	)

	store, err := snapshot.NewSnapshotValidatorStore(
		hclog.NewNullLogger(),
		&store.MockBlockchain{
			HeaderFn: func() *types.Header {
				return &types.Header{Number: 10}
			},
		},
		func(u uint64) (snapshot.SignerInterface, error) {
			return nil, nil
		},
		epochSize,
		metadata,
		snapshots,
	)

	assert.NoError(t, err)

	wrapper := SnapshotValidatorStoreWrapper{
		dirPath:                dirPath,
		SnapshotValidatorStore: store,
	}

	assert.NoError(t, wrapper.Close())

	savedMetadataFile, err := os.ReadFile(path.Join(dirPath, snapshotMetadataFilename))
	assert.NoError(t, err)
	assert.JSONEq(
		t,
		createTestMetadataJSON(metadata.LastBlock),
		string(savedMetadataFile),
	)

	savedSnapshots, err := os.ReadFile(path.Join(dirPath, snapshotSnapshotsFilename))
	assert.NoError(t, err)
	assert.JSONEq(
		t,
		fmt.Sprintf("[%s]", createTestSnapshotJSON(t, snapshots[0])),
		string(savedSnapshots),
	)
}

type MockExecutor struct {
	BeginTxnFunc func(types.Hash, *types.Header, types.Address) (*state.Transition, error)
}

func (m *MockExecutor) BeginTxn(hash types.Hash, header *types.Header, addr types.Address) (*state.Transition, error) {
	return m.BeginTxnFunc(hash, header, addr)
}

func TestNewContractValidatorStoreWrapper(t *testing.T) {
	t.Parallel()

	_, err := NewContractValidatorStoreWrapper(
		hclog.NewNullLogger(),
		&store.MockBlockchain{},
		&MockExecutor{},
		func(u uint64) (signer.Signer, error) {
			return nil, nil
		},
		"",
	)

	assert.NoError(t, err)
}

func TestNewContractValidatorStoreWrapperClose(t *testing.T) {
	t.Parallel()

	wrapper, err := NewContractValidatorStoreWrapper(
		hclog.NewNullLogger(),
		&store.MockBlockchain{},
		&MockExecutor{},
		func(u uint64) (signer.Signer, error) {
			return nil, nil
		},
		"",
	)

	assert.NoError(t, err)
	assert.NoError(t, wrapper.Close())
}

func TestNewContractValidatorStoreWrapperGetValidators(t *testing.T) {
	t.Parallel()

	t.Run("should return error if getSigner returns error", func(t *testing.T) {
		t.Parallel()

		wrapper, err := NewContractValidatorStoreWrapper(
			hclog.NewNullLogger(),
			&store.MockBlockchain{},
			&MockExecutor{},
			func(u uint64) (signer.Signer, error) {
				return nil, errTest
			},
			t.TempDir(),
		)

		assert.NoError(t, err)

		res, err := wrapper.GetValidators(1, 10, 0)
		assert.Nil(t, res)
		assert.ErrorIs(t, errTest, err)
	})

	t.Run("should return error if GetValidatorsByHeight returns error", func(t *testing.T) {
		t.Parallel()

		wrapper, err := NewContractValidatorStoreWrapper(
			hclog.NewNullLogger(),
			&store.MockBlockchain{
				GetHeaderByNumberFn: func(u uint64) (*types.Header, bool) {
					return nil, false
				},
			},
			&MockExecutor{},
			func(u uint64) (signer.Signer, error) {
				return signer.NewSigner(
					&signer.ECDSAKeyManager{},
					nil,
				), nil
			},
			t.TempDir(),
		)

		assert.NoError(t, err)

		res, err := wrapper.GetValidators(10, 10, 0)
		assert.Nil(t, res)
		assert.ErrorContains(t, err, "failed to load parent header for validator snapshot")
	})

	t.Run("should return hard error before forkFrom in contract store wrapper", func(t *testing.T) {
		t.Parallel()

		var beginTxnCalls int

		expected := validators.NewECDSAValidatorSet(
			validators.NewECDSAValidator(types.StringToAddress("1")),
		)

		wrapper, err := NewContractValidatorStoreWrapper(
			hclog.NewNullLogger(),
			&store.MockBlockchain{
				GetHeaderByNumberFn: func(u uint64) (*types.Header, bool) {
					if u == 49 {
						return &types.Header{Number: 49}, true
					}

					return nil, false
				},
			},
			&MockExecutor{
				BeginTxnFunc: func(types.Hash, *types.Header, types.Address) (*state.Transition, error) {
					beginTxnCalls++

					return nil, errTest
				},
			},
			func(u uint64) (signer.Signer, error) {
				return &mockSigner{
					TypeFn: func() validators.ValidatorType {
						return validators.ECDSAValidatorType
					},
					GetValidatorsFn: func(*types.Header) (validators.Validators, error) {
						return expected, nil
					},
				}, nil
			},
			t.TempDir(),
		)

		assert.NoError(t, err)

		res, err := wrapper.GetValidators(10, 10, 50)
		assert.Nil(t, res)
		assert.ErrorContains(t, err, "failed to load parent header for validator snapshot")
		assert.Zero(t, beginTxnCalls)
	})

	t.Run("should use parent header validators at cutover block and not read pos snapshot", func(t *testing.T) {
		t.Parallel()

		var beginTxnCalls int
		expected := validators.NewBLSValidatorSet(
			validators.NewBLSValidator(types.StringToAddress("0x1000000000000000000000000000000000000001"), []byte{1}),
		)

		wrapper, err := NewContractValidatorStoreWrapper(
			hclog.NewNullLogger(),
			&store.MockBlockchain{
				GetHeaderByNumberFn: func(u uint64) (*types.Header, bool) {
					if u == 9 {
						return &types.Header{Number: 9}, true
					}

					return nil, false
				},
			},
			&MockExecutor{
				BeginTxnFunc: func(types.Hash, *types.Header, types.Address) (*state.Transition, error) {
					beginTxnCalls++

					return nil, errTest
				},
			},
			func(u uint64) (signer.Signer, error) {
				return &mockSigner{
					TypeFn: func() validators.ValidatorType {
						return validators.BLSValidatorType
					},
					GetValidatorsFn: func(*types.Header) (validators.Validators, error) {
						return expected, nil
					},
				}, nil
			},
			t.TempDir(),
		)

		assert.NoError(t, err)

		res, err := wrapper.GetValidators(10, 10, 10)
		assert.NoError(t, err)
		assert.True(t, res.Equal(expected))
		assert.Equal(t, 0, beginTxnCalls)
	})

	t.Run("should use parent header validators at genesis boundary in pure pos mode", func(t *testing.T) {
		t.Parallel()

		var beginTxnCalls int
		expected := validators.NewECDSAValidatorSet(
			validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000001")),
		)

		wrapper, err := NewContractValidatorStoreWrapper(
			hclog.NewNullLogger(),
			&store.MockBlockchain{
				GetHeaderByNumberFn: func(u uint64) (*types.Header, bool) {
					if u == 0 {
						return &types.Header{Number: 0}, true
					}

					return nil, false
				},
			},
			&MockExecutor{
				BeginTxnFunc: func(types.Hash, *types.Header, types.Address) (*state.Transition, error) {
					beginTxnCalls++

					return nil, errTest
				},
			},
			func(u uint64) (signer.Signer, error) {
				return &mockSigner{
					TypeFn: func() validators.ValidatorType {
						return validators.ECDSAValidatorType
					},
					GetValidatorsFn: func(*types.Header) (validators.Validators, error) {
						return expected, nil
					},
				}, nil
			},
			t.TempDir(),
		)

		require.NoError(t, err)

		res, err := wrapper.GetValidators(1, 10, 0)
		require.NoError(t, err)
		require.True(t, res.Equal(expected))
		require.Zero(t, beginTxnCalls)
	})

	t.Run("should read pos snapshot after cutover", func(t *testing.T) {
		t.Parallel()

		txn := macroTestTransition(t)
		epoch := uint64(2)
		expected := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000011")))
		require.NoError(t, pos.StoreMacroEpochValidatorSet(txn, epoch, expected))
		header := &types.Header{Number: 10}
		wrapper, err := NewContractValidatorStoreWrapper(
			hclog.NewNullLogger(),
			&store.MockBlockchain{
				GetHeaderByNumberFn: func(u uint64) (*types.Header, bool) {
					if u == 10 {
						return header, true
					}
					return nil, false
				},
			},
			&MockExecutor{
				BeginTxnFunc: func(hash types.Hash, h *types.Header, _ types.Address) (*state.Transition, error) {
					return txn, nil
				},
			},
			func(u uint64) (signer.Signer, error) {
				return &mockSigner{TypeFn: func() validators.ValidatorType { return validators.ECDSAValidatorType }}, nil
			},
			t.TempDir(),
		)
		require.NoError(t, err)
		res, err := wrapper.GetValidators(11, 10, 10)
		require.NoError(t, err)
		require.True(t, res.Equal(expected))
	})

	t.Run("should hard fail if post-cutover snapshot missing", func(t *testing.T) {
		t.Parallel()
		header := &types.Header{Number: 10}
		wrapper, err := NewContractValidatorStoreWrapper(
			hclog.NewNullLogger(),
			&store.MockBlockchain{GetHeaderByNumberFn: func(u uint64) (*types.Header, bool) { return header, true }},
			&MockExecutor{BeginTxnFunc: func(types.Hash, *types.Header, types.Address) (*state.Transition, error) { return macroTestTransition(t), nil }},
			func(u uint64) (signer.Signer, error) { return &mockSigner{TypeFn: func() validators.ValidatorType { return validators.ECDSAValidatorType }}, nil },
			t.TempDir(),
		)
		require.NoError(t, err)
		res, err := wrapper.GetValidators(11, 10, 10)
		require.Nil(t, res)
		require.ErrorIs(t, err, errMissingCanonicalSnapshot)
	})
}

func TestContractValidatorStoreWrapper_GetStakeSnapshotCutoverHardError(t *testing.T) {
	t.Parallel()

	wrapper, err := NewContractValidatorStoreWrapper(
		hclog.NewNullLogger(),
		&store.MockBlockchain{},
		&MockExecutor{},
		func(u uint64) (signer.Signer, error) { return nil, nil },
		t.TempDir(),
	)
	require.NoError(t, err)

	stake, err := wrapper.GetStakeSnapshot(10, 10, 10, validators.NewECDSAValidatorSet(validators.NewECDSAValidator(types.StringToAddress("0x1"))))
	require.Nil(t, stake)
	require.ErrorContains(t, err, "stake snapshot is unavailable at cutover block")
}

func TestMacroSnapshotEpoch(t *testing.T) {
	t.Parallel()

	epoch, err := macroSnapshotEpoch(10, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(1), epoch)

	epoch, err = macroSnapshotEpoch(11, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(2), epoch)

	epoch, err = macroSnapshotEpoch(20, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(2), epoch)

	_, err = macroSnapshotEpoch(0, 10)
	require.Error(t, err)
}
