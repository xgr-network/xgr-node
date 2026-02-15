// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

/**
 * @title EngineRegistry
 * @notice On-chain registry for authorized engine addresses and chain parameters.
 * @dev This contract provides deterministic, on-chain storage for:
 *      - Authorized engine EOAs (multiple supported for load balancing)
 *      - MinBaseFee parameter (governance-updateable)
 *      - Future extensibility for additional chain parameters
 * 
 * The precompile queries this contract via staticcall. If the call fails or
 * the contract is not deployed, the precompile falls back to default values.
 */
contract EngineRegistry {
    // =========================================================================
    // State Variables
    // =========================================================================

    // Storage layout notes (for Go slot reads via SLOAD):
    //   slot 0: admin
    //   slot 1: pendingAdmin
    //   slot 2: authorizedEngines (mapping)
    //   slot 3: engineList (dynamic array)
    //   slot 4: engineIndex (mapping)
    //   slot 5: minBaseFee
    //   slot 6: paused (bool)
    //   slot 7: __reserved0 (uint256)  <-- forces next vars onto fresh slots (no packing with bool)
    //   slot 8: donationAddress
    //   slot 9: donationPercent    
    /// @notice Admin address (should be multisig or governance contract)
    address public admin;
    
    /// @notice Pending admin for two-step transfer
    address public pendingAdmin;
    
    /// @notice Mapping of authorized engine addresses
    mapping(address => bool) public authorizedEngines;
    
    /// @notice Array of all engine addresses (for enumeration)
    address[] public engineList;
    
    /// @notice Index tracking for efficient removal
    mapping(address => uint256) private engineIndex;
    
    /// @notice Minimum base fee in wei (default: 100 Gwei)
    uint256 public minBaseFee;
    
    /// @notice Emergency pause flag
    bool public paused;

    /// @dev Reserved full slot to avoid storage packing after `paused` (bool)
    uint256 private __reserved0;

    /// @notice Donation recipient address (can be a donation contract)
    address public donationAddress;

    /// @notice Donation fee percent in [0..100]. (0 disables donation)
    uint256 public donationPercent;
    
    // =========================================================================
    // Constants
    // =========================================================================
    
    /// @notice Default minimum base fee (100 Gwei)
    uint256 public constant DEFAULT_MIN_BASE_FEE = 100_000_000_000;

    /// @notice Default donation percent (0 => disabled until explicitly configured)
    uint256 public constant DEFAULT_DONATION_PERCENT = 0;

    /// @notice Maximum allowed engines to prevent gas issues
    uint256 public constant MAX_ENGINES = 200;
    
    // =========================================================================
    // Events
    // =========================================================================
    
    event EngineAdded(address indexed engine, address indexed addedBy);
    event EngineRemoved(address indexed engine, address indexed removedBy);
    event MinBaseFeeUpdated(uint256 oldFee, uint256 newFee, address indexed updatedBy);
    event DonationConfigUpdated(address indexed donationAddress, uint256 donationPercent, address indexed updatedBy);
    event AdminTransferInitiated(address indexed currentAdmin, address indexed pendingAdmin);
    event AdminTransferCompleted(address indexed oldAdmin, address indexed newAdmin);
    event Paused(address indexed by);
    event Unpaused(address indexed by);
    
    // =========================================================================
    // Errors
    // =========================================================================
    
    error NotAdmin();
    error NotPendingAdmin();
    error ZeroAddress();
    error EngineAlreadyAuthorized(address engine);
    error EngineNotAuthorized(address engine);
    error MaxEnginesReached();
    error ContractPaused();
    error InvalidMinBaseFee();
    error InvalidDonationPercent();
    
    // =========================================================================
    // Modifiers
    // =========================================================================
    
    modifier onlyAdmin() {
        if (msg.sender != admin) revert NotAdmin();
        _;
    }
    
    modifier whenNotPaused() {
        if (paused) revert ContractPaused();
        _;
    }
    
    // =========================================================================
    // Constructor
    // =========================================================================
    
    /**
     * @notice Initialize the registry with admin, initial engines, and min base fee
     * @param _admin Admin address (should be multisig)
     * @param _initialEngines Array of initially authorized engine addresses
     * @param _minBaseFee Initial minimum base fee (use 0 for default)
     */
    constructor(
        address _admin,
        address[] memory _initialEngines,
        uint256 _minBaseFee
    ) {
        if (_admin == address(0)) revert ZeroAddress();
        
        admin = _admin;
        minBaseFee = _minBaseFee > 0 ? _minBaseFee : DEFAULT_MIN_BASE_FEE;

        // Donation config defaults: disabled until explicitly set by admin.
        // We initialize `donationAddress` to a non-zero value to avoid accidental burns
        // if chain code starts reading donation config immediately after deployment.
        donationAddress = _admin;
        donationPercent = DEFAULT_DONATION_PERCENT;
        
        for (uint256 i = 0; i < _initialEngines.length; i++) {
            address engine = _initialEngines[i];
            if (engine == address(0)) revert ZeroAddress();
            if (authorizedEngines[engine]) revert EngineAlreadyAuthorized(engine);
            
            authorizedEngines[engine] = true;
            engineIndex[engine] = engineList.length;
            engineList.push(engine);
            
            emit EngineAdded(engine, _admin);
        }
    }
    
    // =========================================================================
    // View Functions (called by precompile via staticcall)
    // =========================================================================
    
    /**
     * @notice Check if an address is an authorized engine
     * @param engine Address to check
     * @return bool True if authorized
     * @dev This is the primary function called by the precompile
     */
    function isAuthorizedEngine(address engine) external view returns (bool) {
        // Note: Returns false when paused for safety
        if (paused) return false;
        return authorizedEngines[engine];
    }
    
    /**
     * @notice Get the current minimum base fee
     * @return uint256 Minimum base fee in wei
     * @dev Called by chain code to get dynamic MinBaseFee
     */
    function getMinBaseFee() external view returns (uint256) {
        return minBaseFee;
    }
    
    /**
     * @notice Get the current donation configuration
     * @return addr Donation address
     * @return percent Donation percent in [0..100]
     */
    function getDonationConfig() external view returns (address addr, uint256 percent) {
        return (donationAddress, donationPercent);
    }

    /**
     * @notice Get the number of authorized engines
     * @return uint256 Count of engines
     */
    function getEngineCount() external view returns (uint256) {
        return engineList.length;
    }
    
    /**
     * @notice Get all authorized engine addresses
     * @return address[] Array of engine addresses
     */
    function getAllEngines() external view returns (address[] memory) {
        return engineList;
    }
    
    /**
     * @notice Check multiple engines at once (gas efficient for batch checks)
     * @param engines Array of addresses to check
     * @return results Array of authorization status
     */
    function areEnginesAuthorized(address[] calldata engines) 
        external 
        view 
        returns (bool[] memory results) 
    {
        results = new bool[](engines.length);
        for (uint256 i = 0; i < engines.length; i++) {
            results[i] = !paused && authorizedEngines[engines[i]];
        }
    }
    
    // =========================================================================
    // Admin Functions - Engine Management
    // =========================================================================
    
    /**
     * @notice Add a new authorized engine
     * @param engine Address to authorize
     */
    function addEngine(address engine) external onlyAdmin whenNotPaused {
        if (engine == address(0)) revert ZeroAddress();
        if (authorizedEngines[engine]) revert EngineAlreadyAuthorized(engine);
        if (engineList.length >= MAX_ENGINES) revert MaxEnginesReached();
        
        authorizedEngines[engine] = true;
        engineIndex[engine] = engineList.length;
        engineList.push(engine);
        
        emit EngineAdded(engine, msg.sender);
    }
    
    /**
     * @notice Add multiple engines at once
     * @param engines Array of addresses to authorize
     */
    function addEngines(address[] calldata engines) external onlyAdmin whenNotPaused {
        if (engineList.length + engines.length > MAX_ENGINES) revert MaxEnginesReached();
        
        for (uint256 i = 0; i < engines.length; i++) {
            address engine = engines[i];
            if (engine == address(0)) revert ZeroAddress();
            if (authorizedEngines[engine]) revert EngineAlreadyAuthorized(engine);
            
            authorizedEngines[engine] = true;
            engineIndex[engine] = engineList.length;
            engineList.push(engine);
            
            emit EngineAdded(engine, msg.sender);
        }
    }
    
    /**
     * @notice Remove an authorized engine
     * @param engine Address to deauthorize
     */
    function removeEngine(address engine) external onlyAdmin {
        if (!authorizedEngines[engine]) revert EngineNotAuthorized(engine);
        
        authorizedEngines[engine] = false;
        
        // Swap and pop for efficient removal
        uint256 index = engineIndex[engine];
        uint256 lastIndex = engineList.length - 1;
        
        if (index != lastIndex) {
            address lastEngine = engineList[lastIndex];
            engineList[index] = lastEngine;
            engineIndex[lastEngine] = index;
        }
        
        engineList.pop();
        delete engineIndex[engine];
        
        emit EngineRemoved(engine, msg.sender);
    }
    
    /**
     * @notice Replace an engine address (atomic remove + add)
     * @param oldEngine Engine to remove
     * @param newEngine Engine to add
     * @dev Useful when rotating engine keys
     */
    function replaceEngine(address oldEngine, address newEngine) external onlyAdmin whenNotPaused {
        if (newEngine == address(0)) revert ZeroAddress();
        if (!authorizedEngines[oldEngine]) revert EngineNotAuthorized(oldEngine);
        if (authorizedEngines[newEngine]) revert EngineAlreadyAuthorized(newEngine);
        
        // Remove old
        authorizedEngines[oldEngine] = false;
        uint256 index = engineIndex[oldEngine];
        delete engineIndex[oldEngine];
        
        // Add new at same position
        authorizedEngines[newEngine] = true;
        engineList[index] = newEngine;
        engineIndex[newEngine] = index;
        
        emit EngineRemoved(oldEngine, msg.sender);
        emit EngineAdded(newEngine, msg.sender);
    }
    
    // =========================================================================
    // Admin Functions - Parameter Management
    // =========================================================================
    
    /**
     * @notice Update the minimum base fee
     * @param newMinBaseFee New minimum base fee in wei
     */
    function setMinBaseFee(uint256 newMinBaseFee) external onlyAdmin {
        // Allow 0 to effectively disable minimum (though not recommended)
        // Sanity check: don't allow absurdly high values (> 10000 Gwei)
        if (newMinBaseFee > 10_000_000_000_000) revert InvalidMinBaseFee();
        
        uint256 oldFee = minBaseFee;
        minBaseFee = newMinBaseFee;
        
        emit MinBaseFeeUpdated(oldFee, newMinBaseFee, msg.sender);
    }

    /**
     * @notice Update donation configuration (address + percent)
     * @param addr Donation recipient address (typically a donation contract)
     * @param percent Donation percent in [0..100]. 0 disables donation.
     */
    function setDonationConfig(address addr, uint256 percent) external onlyAdmin {
        if (percent > 100) revert InvalidDonationPercent();
        if (addr == address(0)) revert ZeroAddress();

        donationAddress = addr;
        donationPercent = percent;

        emit DonationConfigUpdated(addr, percent, msg.sender);
    }

    // =========================================================================
    // Admin Functions - Access Control
    // =========================================================================
    
    /**
     * @notice Initiate admin transfer (two-step for safety)
     * @param newAdmin New admin address
     */
    function transferAdmin(address newAdmin) external onlyAdmin {
        if (newAdmin == address(0)) revert ZeroAddress();
        
        pendingAdmin = newAdmin;
        emit AdminTransferInitiated(admin, newAdmin);
    }
    
    /**
     * @notice Accept admin role (must be called by pending admin)
     */
    function acceptAdmin() external {
        if (msg.sender != pendingAdmin) revert NotPendingAdmin();
        
        address oldAdmin = admin;
        admin = pendingAdmin;
        pendingAdmin = address(0);
        
        emit AdminTransferCompleted(oldAdmin, admin);
    }
    
    /**
     * @notice Cancel pending admin transfer
     */
    function cancelAdminTransfer() external onlyAdmin {
        pendingAdmin = address(0);
    }
    
    // =========================================================================
    // Admin Functions - Emergency
    // =========================================================================
    
    /**
     * @notice Pause the registry (all isAuthorizedEngine calls return false)
     * @dev Use in emergency if engine keys are compromised
     */
    function pause() external onlyAdmin {
        paused = true;
        emit Paused(msg.sender);
    }
    
    /**
     * @notice Unpause the registry
     */
    function unpause() external onlyAdmin {
        paused = false;
        emit Unpaused(msg.sender);
    }
}
