package fork

import (
	"encoding/json"
	"errors"
	"math/big"
	"path/filepath"

	"github.com/hashicorp/go-hclog"
	"github.com/xgr-network/xgr-node/consensus/ibft/pos"
	"github.com/xgr-network/xgr-node/consensus/ibft/signer"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
	"github.com/xgr-network/xgr-node/validators/store"
	"github.com/xgr-network/xgr-node/validators/store/contract"
	"github.com/xgr-network/xgr-node/validators/store/snapshot"
)

var (
	errMissingCanonicalSnapshot = errors.New("missing canonical epoch validator snapshot")
	errHeightInvalid            = errors.New("height must be greater than 0")
	errEpochSizeInvalid         = errors.New("epochSize must be greater than or equal to 2")
)

func isJSONSyntaxError(err error) bool {
	var expected *json.SyntaxError

	if err == nil {
		return false
	}

	return errors.As(err, &expected)
}

type SnapshotValidatorStoreWrapper struct {
	*snapshot.SnapshotValidatorStore
	dirPath string
}

func (w *SnapshotValidatorStoreWrapper) Close() error {
	metadata := w.GetSnapshotMetadata()
	snapshots := w.GetSnapshots()

	if err := writeDataStore(filepath.Join(w.dirPath, snapshotMetadataFilename), metadata); err != nil {
		return err
	}

	if err := writeDataStore(filepath.Join(w.dirPath, snapshotSnapshotsFilename), snapshots); err != nil {
		return err
	}

	return nil
}

func (w *SnapshotValidatorStoreWrapper) GetValidators(height, _, _ uint64) (validators.Validators, error) {
	if height == 0 {
		return nil, errHeightInvalid
	}

	return w.GetValidatorsByHeight(height - 1)
}

func NewSnapshotValidatorStoreWrapper(logger hclog.Logger, blockchain store.HeaderGetter, getSigner func(uint64) (signer.Signer, error), dirPath string, epochSize uint64) (*SnapshotValidatorStoreWrapper, error) {
	snapshotMetadataPath := filepath.Join(dirPath, snapshotMetadataFilename)
	snapshotsPath := filepath.Join(dirPath, snapshotSnapshotsFilename)

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

	snapshotStore, err := snapshot.NewSnapshotValidatorStore(logger, blockchain, func(height uint64) (snapshot.SignerInterface, error) {
		rawSigner, err := getSigner(height)
		if err != nil {
			return nil, err
		}

		return snapshot.SignerInterface(rawSigner), nil
	}, epochSize, snapshotMeta, snapshots)
	if err != nil {
		return nil, err
	}

	return &SnapshotValidatorStoreWrapper{SnapshotValidatorStore: snapshotStore, dirPath: dirPath}, nil
}

type ContractValidatorStoreWrapper struct {
	*contract.ContractValidatorStore
	blockchain store.HeaderGetter
	executor   contract.Executor
	getSigner  func(uint64) (signer.Signer, error)
}

func NewContractValidatorStoreWrapper(logger hclog.Logger, blockchain store.HeaderGetter, executor contract.Executor, getSigner func(uint64) (signer.Signer, error), _ string) (*ContractValidatorStoreWrapper, error) {
	contractStore, err := contract.NewContractValidatorStore(logger, blockchain, executor, contract.DefaultValidatorSetCacheSize)
	if err != nil {
		return nil, err
	}

	return &ContractValidatorStoreWrapper{ContractValidatorStore: contractStore, blockchain: blockchain, executor: executor, getSigner: getSigner}, nil
}

func (w *ContractValidatorStoreWrapper) Close() error {
	return nil
}

func (w *ContractValidatorStoreWrapper) GetStakeSnapshot(height uint64, epochSize uint64, forkFrom uint64, validatorSet validators.Validators) (map[types.Address]*big.Int, error) {
	if forkFrom > 0 && height == forkFrom {
		return nil, errors.New("stake snapshot is unavailable at cutover block")
	}

	epoch, err := macroSnapshotEpoch(height, epochSize)
	if err != nil {
		return nil, err
	}
	if height == 0 {
		return nil, errHeightInvalid
	}
	parentHeight := height - 1
	header, ok := w.blockchain.GetHeaderByNumber(parentHeight)
	if !ok {
		return nil, errors.New("failed to load parent header for stake snapshot")
	}
	tx, err := w.executor.BeginTxn(header.StateRoot, header, types.ZeroAddress)
	if err != nil {
		return nil, err
	}
	stake, ok, err := pos.LoadMacroEpochStakeSnapshot(tx, epoch, validatorSet)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("missing canonical epoch validator stake snapshot")
	}
	return stake, nil
}

func (w *ContractValidatorStoreWrapper) GetValidators(height, epochSize, forkFrom uint64) (validators.Validators, error) {
	epoch, err := macroSnapshotEpoch(height, epochSize)
	if err != nil {
		return nil, err
	}

	if height == 0 {
		return nil, errHeightInvalid
	}
	parentHeight := height - 1
	s, err := w.getSigner(parentHeight)
	if err != nil {
		return nil, err
	}
	header, ok := w.blockchain.GetHeaderByNumber(parentHeight)
	if !ok {
		return nil, errors.New("failed to load parent header for validator snapshot")
	}
	if (forkFrom > 0 && height == forkFrom) || (forkFrom == 0 && height == 1) {
		vals, err := s.GetValidators(header)
		if err != nil {
			return nil, err
		}
		if vals == nil || vals.Len() == 0 {
			return nil, errors.New("header validator set is empty")
		}
		return vals, nil
	}

	tx, err := w.executor.BeginTxn(header.StateRoot, header, types.ZeroAddress)
	if err != nil {
		return nil, err
	}
	vals, ok, err := pos.LoadMacroEpochValidatorSet(tx, epoch, s.Type())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errMissingCanonicalSnapshot
	}
	return vals, nil
}

func macroSnapshotEpoch(height uint64, epochSize uint64) (uint64, error) {
	if height == 0 {
		return 0, errHeightInvalid
	}
	if epochSize < 2 {
		return 0, errEpochSizeInvalid
	}

	// Macro snapshot keying follows PoS epoch numbering (1-based, no epoch 0).
	return ((height - 1) / epochSize) + 1, nil
}
