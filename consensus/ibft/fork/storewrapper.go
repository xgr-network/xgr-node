package fork

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/hashicorp/go-hclog"
	"github.com/xgr-network/xgr-node/consensus/ibft/signer"
	"github.com/xgr-network/xgr-node/validators"
	"github.com/xgr-network/xgr-node/validators/store"
	"github.com/xgr-network/xgr-node/validators/store/contract"
	"github.com/xgr-network/xgr-node/validators/store/snapshot"
)

// isJSONSyntaxError returns bool indicating the giving error is json.SyntaxError or not
func isJSONSyntaxError(err error) bool {
	var expected *json.SyntaxError

	if err == nil {
		return false
	}

	return errors.As(err, &expected)
}

// SnapshotValidatorStoreWrapper is a wrapper of store.SnapshotValidatorStore
// in order to add initialization and closer process with side effect
type SnapshotValidatorStoreWrapper struct {
	*snapshot.SnapshotValidatorStore
	dirPath string
}

// Close saves SnapshotValidator data into local storage
func (w *SnapshotValidatorStoreWrapper) Close() error {
	// save data
	var (
		metadata  = w.GetSnapshotMetadata()
		snapshots = w.GetSnapshots()
	)

	if err := writeDataStore(filepath.Join(w.dirPath, snapshotMetadataFilename), metadata); err != nil {
		return err
	}

	if err := writeDataStore(filepath.Join(w.dirPath, snapshotSnapshotsFilename), snapshots); err != nil {
		return err
	}

	return nil
}

// GetValidators returns validators at the specific height
func (w *SnapshotValidatorStoreWrapper) GetValidators(height, _, _ uint64) (validators.Validators, error) {
	if height == 0 {
		return nil, errors.New("height must be greater than 0")
	}

	// the biggest height of blocks that have been processed before the given height
	return w.GetValidatorsByHeight(height - 1)
}

// NewSnapshotValidatorStoreWrapper loads data from local storage and creates *SnapshotValidatorStoreWrapper
func NewSnapshotValidatorStoreWrapper(
	logger hclog.Logger,
	blockchain store.HeaderGetter,
	getSigner func(uint64) (signer.Signer, error),
	dirPath string,
	epochSize uint64,
) (*SnapshotValidatorStoreWrapper, error) {
	var (
		snapshotMetadataPath = filepath.Join(dirPath, snapshotMetadataFilename)
		snapshotsPath        = filepath.Join(dirPath, snapshotSnapshotsFilename)
	)

	snapshotMeta, err := loadSnapshotMetadata(snapshotMetadataPath)
	if isJSONSyntaxError(err) {
		logger.Warn("Snapshot metadata file is broken, recover metadata from local chain", "filepath", snapshotMetadataPath)

		snapshotMeta = nil
	} else if err != nil {
		return nil, err
	}

	snapshots, err := loadSnapshots(snapshotsPath)
	if isJSONSyntaxError(err) {
		logger.Warn("Snapshots file is broken, recover snapshots from local chain", "filepath", snapshotsPath)

		snapshots = nil
	} else if err != nil {
		return nil, err
	}

	snapshotStore, err := snapshot.NewSnapshotValidatorStore(
		logger,
		blockchain,
		func(height uint64) (snapshot.SignerInterface, error) {
			rawSigner, err := getSigner(height)
			if err != nil {
				return nil, err
			}

			return snapshot.SignerInterface(rawSigner), nil
		},
		epochSize,
		snapshotMeta,
		snapshots,
	)

	if err != nil {
		return nil, err
	}

	return &SnapshotValidatorStoreWrapper{
		SnapshotValidatorStore: snapshotStore,
		dirPath:                dirPath,
	}, nil
}

// ContractValidatorStoreWrapper is a wrapper of *contract.ContractValidatorStore
// in order to add Close and GetValidators
type ContractValidatorStoreWrapper struct {
	*contract.ContractValidatorStore
	blockchain store.HeaderGetter
	getSigner  func(uint64) (signer.Signer, error)
}

// NewContractValidatorStoreWrapper creates *ContractValidatorStoreWrapper
func NewContractValidatorStoreWrapper(
	logger hclog.Logger,
	blockchain store.HeaderGetter,
	executor contract.Executor,
	getSigner func(uint64) (signer.Signer, error),
) (*ContractValidatorStoreWrapper, error) {
	contractStore, err := contract.NewContractValidatorStore(
		logger,
		blockchain,
		executor,
		contract.DefaultValidatorSetCacheSize,
	)

	if err != nil {
		return nil, err
	}

	return &ContractValidatorStoreWrapper{
		ContractValidatorStore: contractStore,
		blockchain:             blockchain,
		getSigner:              getSigner,
	}, nil
}

// Close is closer process
func (w *ContractValidatorStoreWrapper) Close() error {
	return nil
}

// GetValidators gets and returns validators at the given height
func (w *ContractValidatorStoreWrapper) GetValidators(
	height,
	epochSize,
	forkFrom uint64,
) (validators.Validators, error) {
	signer, err := w.getSigner(height)
	if err != nil {
		return nil, err
	}

	if epochSize < 2 {
		return nil, errors.New("epochSize must be greater than or equal to 2")
	}

	fetchingHeight := calculateContractStoreFetchingHeight(
		height,
		epochSize,
		forkFrom,
	)

	// Before PoS fork, contract store must not be used as validator source.
	if height < forkFrom {
		headerHeight := uint64(0)
		if height > 0 {
			headerHeight = height - 1
		}

		headerSigner, signErr := w.getSigner(headerHeight)
		if signErr != nil {
			return nil, signErr
		}

		header, ok := w.blockchain.GetHeaderByNumber(headerHeight)
		if !ok {
			return nil, errors.New("failed to load pre-fork header validators")
		}

		return headerSigner.GetValidators(header)
	}

	vals, err := w.GetValidatorsByHeight(
		signer.Type(),
		fetchingHeight,
	)
	if err == nil && vals != nil && vals.Len() > 0 {
		return vals, nil
	}

	// Before / at PoS fork boundary we may need to use pre-fork header validator set.
	// Reason: the staking contract is deployed at the PoS deployment block, but the block itself
	// must still be validated using the PoS fork boundary validator source.
	if height <= forkFrom {
		if err != nil && !strings.Contains(err.Error(), "empty input") {
			return nil, err
		}

		headerSigner, signErr := w.getSigner(fetchingHeight)
		if signErr != nil {
			if err != nil {
				return nil, err
			}

			return nil, signErr
		}

		header, ok := w.blockchain.GetHeaderByNumber(fetchingHeight)
		if !ok {
			if err != nil {
				return nil, err
			}
			return nil, errors.New("failed to load pre-fork header validators")
		}

		fromHeader, hdrErr := headerSigner.GetValidators(header)
		if hdrErr != nil {
			if err != nil {
				return nil, err
			}
			return nil, hdrErr
		}

		return fromHeader, nil
	}

	// After PoS fork, staking contract is the source of truth.
	if err != nil {
		return nil, err
	}

	return nil, errors.New("staking contract returned empty validator set")
}

// calculateContractStoreFetchingHeight calculates the block height at which ContractStore fetches validators
// based on height, epoch, and fork beginning height
func calculateContractStoreFetchingHeight(
	height uint64,
	epochSize uint64,
	forkFrom uint64,
) uint64 {
	var fetchingHeight uint64

	if height == 0 {
		fetchingHeight = 0
	} else if height%epochSize == 0 {
		// Epoch boundary block itself is finalized with the previous epoch context.
		fetchingHeight = height - 1
	} else {
		// Non-boundary blocks should read validator data at the epoch boundary block,
		// because finalize hooks on that boundary can affect the effective next-epoch set.
		fetchingHeight = (height / epochSize) * epochSize
	}

	// For all heights up to and including the PoS forkFrom block, keep legacy behavior
	// (contract fetch height pinned to the last PoA block) so the PoS deployment block can
	// still be validated with the PoA validator set.
	if height <= forkFrom {
		if forkFrom == 0 {
			return 0
		}
		return forkFrom - 1
	}

	// After the PoS fork, never fetch from a pre-fork height.
	if fetchingHeight < forkFrom {
		return forkFrom
	}

	return fetchingHeight
}
