// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

contract StakingV2 {
    uint256 public constant VALIDATOR_THRESHOLD_TOTAL = 2_000_000 ether;
    uint256 public constant VALIDATOR_MIN_SELF_STAKE = 200_000 ether;
    uint256 public constant DELEGATOR_MIN_STAKE = 10_000 ether;
    uint256 public constant MAX_DELEGATORS_PER_VALIDATOR = 200;
    // Backward-compatible alias
    uint256 public constant MIN_STAKE = VALIDATOR_THRESHOLD_TOTAL;

    uint256 public VALIDATOR_THRESHOLD;
    uint64 public epochSize;
    uint64 public minNumValidators;
    uint64 public maxNumValidators;

    struct Staker {
        bool exists;
        bool active;
        uint64 index;
        uint256 amount;
        uint256 joinedAtBlock;
        uint256 deactivatedAtBlock;
        address validator;
        bytes blsPubKey;
    }

    struct ValidatorPoolConfig {
        bool exists;
        bool active;
        bool delegationEnabled;
        uint256 maxTotalDelegatedStake;
        uint256 minDelegatorStake;
        uint16 commissionBps;
        bytes blsPubKey;
    }

    address[] private _validators;
    mapping(address => Staker) private _staker;
    address[] private _stakers;
    mapping(address => ValidatorPoolConfig) private _poolConfig;
    mapping(address => address[]) private _stakersByValidator;
    mapping(address => mapping(address => uint256)) private _stakerIndexPlusOneByValidator;
    mapping(address => uint256) private _validatorDelegatedStakeRaw;
    mapping(address => uint256) private _validatorDelegatedStakeActive;
    uint256 private _reentrancyStatus;

    event Staked(address indexed account, uint256 amount);
    event Unstaked(address indexed account, uint256 amount);
    event ValidatorActivationChanged(address indexed account, bool active, uint256 blockNumber);
    event ValidatorRemoved(address indexed account);
    event ValidatorPoolConfigUpdated(
        address indexed validator,
        bool delegationEnabled,
        uint256 maxTotalDelegatedStake,
        uint256 minDelegatorStake,
        uint16 commissionBps
    );
    event Withdrawn(address indexed account, uint256 amount);

    error InvalidEpochSize();
    error JoinStakeTooLow();
    error ValidatorNotFound();
    error AlreadyInRequestedState();
    error MustDeactivateBeforeUnstake();
    error UnstakeTooEarly();
    error NoStake();
    error InvalidWithdrawAmount();
    error InsufficientStakeAfterWithdraw();
    error InvalidValidatorTarget();
    error DelegationPoolClosed();
    error DelegationPoolMaxExceeded();
    error InvalidCommissionBps();
    error InvalidMinDelegatorStake();
    error DelegatorLimitExceeded();

    modifier nonReentrant() {
        require(_reentrancyStatus == 0, "REENTRANT");
        _reentrancyStatus = 1;
        _;
        _reentrancyStatus = 0;
    }

    constructor(uint64 _minNumValidators, uint64 _maxNumValidators, uint64 _epochSize) {
        if (_epochSize == 0) revert InvalidEpochSize();
        minNumValidators = _minNumValidators;
        maxNumValidators = _maxNumValidators;
        epochSize = _epochSize;
        VALIDATOR_THRESHOLD = VALIDATOR_THRESHOLD_TOTAL;
    }

    function validators() external view returns (address[] memory) {
        return _validators;
    }

    function validatorBLSPublicKeys() external view returns (bytes[] memory keys) {
        keys = new bytes[](_validators.length);
        for (uint256 i = 0; i < _validators.length; i++) {
            keys[i] = _staker[_validators[i]].blsPubKey;
        }
    }

    function accountStake(address account) external view returns (uint256) {
        return _staker[account].amount;
    }

    function validatorInfo(address account)
        external
        view
        returns (bool exists, bool active, uint256 stakedAmount, uint256 deactivatedAtBlock, bytes memory blsPubKey)
    {
        Staker storage s = _staker[account];
        return (s.exists, s.active, s.amount, s.deactivatedAtBlock, s.blsPubKey);
    }

    function stakerInfo(address account) external view returns (Staker memory) {
        return _staker[account];
    }

    function validatorPool(address validator) external view returns (ValidatorPoolConfig memory) {
        return _poolConfig[validator];
    }

    function delegationEnabled(address validator) external view returns (bool) {
        return _poolConfig[validator].delegationEnabled;
    }

    function validatorSelfStake(address validator) external view returns (uint256) {
        Staker storage s = _staker[validator];
        if (!s.exists || s.validator != validator) return 0;
        return s.amount;
    }

    function validatorDelegatedStakeRaw(address validator) external view returns (uint256) {
        return _validatorDelegatedStakeRaw[validator];
    }

    function validatorDelegatedStakeActive(address validator) external view returns (uint256) {
        return _validatorDelegatedStakeActive[validator];
    }

    function validatorTotalStake(address validator) public view returns (uint256) {
        Staker storage s = _staker[validator];
        if (!s.exists || s.validator != validator) return 0;
        uint256 selfStake = s.active ? s.amount : 0;
        return selfStake + _validatorDelegatedStakeActive[validator];
    }

    function isValidatorThresholdMet(address validator) external view returns (bool) {
        return validatorTotalStake(validator) >= VALIDATOR_THRESHOLD;
    }

    function validatorStakerCount(address validator) external view returns (uint256) {
        return _stakersByValidator[validator].length;
    }

    function validatorStakerAt(address validator, uint256 idx) external view returns (address) {
        return _stakersByValidator[validator][idx];
    }

    function joinedAtBlock(address account) external view returns (uint256) {
        return _staker[account].joinedAtBlock;
    }

    function joinEffectiveAtBlock(address account) external view returns (uint256) {
        uint256 joined = _staker[account].joinedAtBlock;
        if (joined == 0) return 0;
        return (_epochOf(joined) + 1) * uint256(epochSize);
    }

    function deactivationEffectiveAtBlock(address account) external view returns (uint256) {
        Staker storage s = _staker[account];
        if (s.deactivatedAtBlock == 0) return 0;
        return (_epochOf(s.deactivatedAtBlock) + 1) * uint256(epochSize);
    }

    function registerBLSPublicKey(bytes calldata blsPubKey) external nonReentrant {
        Staker storage s = _staker[msg.sender];
        if (!s.exists || s.validator != msg.sender) revert ValidatorNotFound();
        s.blsPubKey = blsPubKey;
        _poolConfig[msg.sender].blsPubKey = blsPubKey;
    }

    function stake() external payable nonReentrant {
        _stakeFor(msg.sender, msg.sender, msg.value);
    }

    function delegate(address validator) external payable nonReentrant {
        _stakeFor(msg.sender, validator, msg.value);
    }

    function setActive(bool active_) external nonReentrant {
        _setStakerActive(msg.sender, msg.sender, active_);
    }

    function setDelegationActive(address validator, bool active_) external nonReentrant {
        _setStakerActive(msg.sender, validator, active_);
    }

    function setValidatorPoolConfig(
        bool delegationEnabled,
        uint256 maxTotalDelegatedStake,
        uint256 minDelegatorStake,
        uint16 commissionBps
    ) external nonReentrant {
        Staker storage s = _staker[msg.sender];
        if (!s.exists || s.validator != msg.sender) revert ValidatorNotFound();
        if (commissionBps > 10000) revert InvalidCommissionBps();
        if (minDelegatorStake != 0 && minDelegatorStake < DELEGATOR_MIN_STAKE) revert InvalidMinDelegatorStake();

        ValidatorPoolConfig storage p = _poolConfig[msg.sender];
        p.delegationEnabled = delegationEnabled;
        p.maxTotalDelegatedStake = maxTotalDelegatedStake;
        p.minDelegatorStake = minDelegatorStake;
        p.commissionBps = commissionBps;

        emit ValidatorPoolConfigUpdated(msg.sender, delegationEnabled, maxTotalDelegatedStake, minDelegatorStake, commissionBps);
    }

    function withdraw(uint256 amount) external nonReentrant {
        _withdrawStaker(msg.sender, msg.sender, amount, false);
    }

    function withdrawDelegation(address validator, uint256 amount) external nonReentrant {
        _withdrawStaker(msg.sender, validator, amount, false);
    }

    function unstake() external nonReentrant {
        _withdrawStaker(msg.sender, msg.sender, 0, true);
    }

    function unstakeDelegation(address validator) external nonReentrant {
        _withdrawStaker(msg.sender, validator, 0, true);
    }



    function _effectiveMinDelegatorStake(address validator) internal view returns (uint256) {
        uint256 minDelegatorStake = _poolConfig[validator].minDelegatorStake;
        return minDelegatorStake == 0 ? DELEGATOR_MIN_STAKE : minDelegatorStake;
    }

    function _delegatorCount(address validator) internal view returns (uint256 count) {
        address[] storage stakers = _stakersByValidator[validator];
        for (uint256 i = 0; i < stakers.length; i++) {
            if (stakers[i] != validator) count++;
        }
    }

    function _stakeFor(address owner, address validator, uint256 amount) internal {
        if (amount == 0) revert NoStake();
        bool selfStake = owner == validator;

        if (!selfStake) {
            Staker storage target = _staker[validator];
            if (!target.exists || target.validator != validator) revert InvalidValidatorTarget();

            ValidatorPoolConfig storage p = _poolConfig[validator];
            if (!p.delegationEnabled) revert DelegationPoolClosed();
            if (_validatorDelegatedStakeRaw[validator] + amount > p.maxTotalDelegatedStake) revert DelegationPoolMaxExceeded();
        }

        Staker storage s = _staker[owner];
        if (!s.exists) {
            uint256 minJoin = selfStake ? VALIDATOR_MIN_SELF_STAKE : _effectiveMinDelegatorStake(validator);
            if (amount < minJoin) revert JoinStakeTooLow();
            if (!selfStake && _delegatorCount(validator) >= MAX_DELEGATORS_PER_VALIDATOR) revert DelegatorLimitExceeded();

            s.exists = true;
            s.active = true;
            s.index = uint64(_stakers.length);
            s.joinedAtBlock = block.number;
            s.validator = validator;
            _stakers.push(owner);

            _stakerIndexPlusOneByValidator[validator][owner] = _stakersByValidator[validator].length + 1;
            _stakersByValidator[validator].push(owner);

            if (selfStake) {
                _validators.push(owner);
                _poolConfig[owner].exists = true;
                _poolConfig[owner].active = true;
                _poolConfig[owner].delegationEnabled = false;
                _poolConfig[owner].maxTotalDelegatedStake = 0;
                _poolConfig[owner].minDelegatorStake = 0;
                _poolConfig[owner].commissionBps = 0;
            }
        } else {
            if (s.validator != validator) revert InvalidValidatorTarget();
        }

        s.amount += amount;
        _syncAggregatorOnStake(owner, validator, amount, s.active);

        emit Staked(owner, amount);
    }

    function _setStakerActive(address owner, address expectedValidator, bool active_) internal {
        Staker storage s = _staker[owner];
        if (!s.exists || s.validator != expectedValidator) revert ValidatorNotFound();
        if (s.active == active_) revert AlreadyInRequestedState();

        uint256 minStake = owner == expectedValidator ? VALIDATOR_MIN_SELF_STAKE : _effectiveMinDelegatorStake(expectedValidator);
        if (active_ && s.amount < minStake) revert InsufficientStakeAfterWithdraw();

        s.active = active_;
        if (active_) {
            s.deactivatedAtBlock = 0;
        } else {
            s.deactivatedAtBlock = block.number;
        }

        if (owner == expectedValidator) {
            _poolConfig[owner].active = active_;
        }

        _syncAggregatorOnToggle(owner, expectedValidator, s.amount, active_);
        emit ValidatorActivationChanged(owner, active_, block.number);
    }

    function _withdrawStaker(address owner, address expectedValidator, uint256 amount, bool fullExit) internal {
        Staker storage s = _staker[owner];
        if (!s.exists || s.validator != expectedValidator) revert ValidatorNotFound();
        if (s.active) revert MustDeactivateBeforeUnstake();
        if (s.deactivatedAtBlock == 0) revert MustDeactivateBeforeUnstake();
        if (_epochOf(block.number) <= _epochOf(s.deactivatedAtBlock)) revert UnstakeTooEarly();

        uint256 payout;
        if (fullExit) {
            payout = s.amount;
        } else {
            if (amount == 0 || amount >= s.amount) revert InvalidWithdrawAmount();
            uint256 minStake = owner == expectedValidator ? VALIDATOR_MIN_SELF_STAKE : _effectiveMinDelegatorStake(expectedValidator);
            uint256 remaining = s.amount - amount;
            if (remaining < minStake) revert InsufficientStakeAfterWithdraw();
            payout = amount;
            s.amount = remaining;
        }

        if (fullExit) {
            _syncAggregatorOnUnstake(owner, expectedValidator, s.amount, s.active);
            if (owner == expectedValidator) {
                _removeValidator(owner);
                delete _poolConfig[owner];
            }
            _removeFromValidatorDelegators(expectedValidator, owner);
            _removeStaker(owner);
            emit Unstaked(owner, payout);
            if (owner == expectedValidator) {
                emit ValidatorRemoved(owner);
            }
        } else {
            _syncAggregatorOnWithdraw(owner, expectedValidator, payout, s.active);
            emit Withdrawn(owner, payout);
        }

        (bool ok, ) = payable(owner).call{value: payout}("");
        require(ok, "transfer failed");
    }

    function _syncAggregatorOnStake(address owner, address validator, uint256 amount, bool active_) internal {
        if (owner == validator) {
            return;
        }

        _validatorDelegatedStakeRaw[validator] += amount;
        if (active_) {
            _validatorDelegatedStakeActive[validator] += amount;
        }
    }

    function _syncAggregatorOnToggle(address owner, address validator, uint256 amount, bool active_) internal {
        if (owner == validator) {
            return;
        }
        if (active_) {
            _validatorDelegatedStakeActive[validator] += amount;
        } else {
            _validatorDelegatedStakeActive[validator] -= amount;
        }
    }

    function _syncAggregatorOnWithdraw(address owner, address validator, uint256 amount, bool active_) internal {
        if (owner == validator) {
            return;
        }
        _validatorDelegatedStakeRaw[validator] -= amount;
        if (active_) {
            _validatorDelegatedStakeActive[validator] -= amount;
        }
    }

    function _syncAggregatorOnUnstake(address owner, address validator, uint256 amount, bool active_) internal {
        if (owner == validator) {
            return;
        }
        _validatorDelegatedStakeRaw[validator] -= amount;
        if (active_) {
            _validatorDelegatedStakeActive[validator] -= amount;
        }
    }

    function _removeValidator(address account) internal {
        uint256 last = _validators.length - 1;
        uint256 idx;
        for (uint256 i = 0; i < _validators.length; i++) {
            if (_validators[i] == account) {
                idx = i;
                break;
            }
        }
        if (idx != last) {
            _validators[idx] = _validators[last];
        }
        _validators.pop();
    }

    function _removeFromValidatorDelegators(address validator, address owner) internal {
        uint256 idxPlusOne = _stakerIndexPlusOneByValidator[validator][owner];
        if (idxPlusOne == 0) return;

        uint256 idx = idxPlusOne - 1;
        uint256 last = _stakersByValidator[validator].length - 1;

        if (idx != last) {
            address moved = _stakersByValidator[validator][last];
            _stakersByValidator[validator][idx] = moved;
            _stakerIndexPlusOneByValidator[validator][moved] = idx + 1;
        }

        _stakersByValidator[validator].pop();
        delete _stakerIndexPlusOneByValidator[validator][owner];
    }

    function _removeStaker(address owner) internal {
        uint256 idx = _staker[owner].index;
        uint256 last = _stakers.length - 1;

        if (idx != last) {
            address moved = _stakers[last];
            _stakers[idx] = moved;
            _staker[moved].index = uint64(idx);
        }

        _stakers.pop();
        delete _staker[owner];
    }

    function _epochOf(uint256 blockNumber) internal view returns (uint256) {
        return blockNumber / uint256(epochSize);
    }
}
