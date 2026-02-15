package engineabi

// Funktions-ABIs (zentral, Single Source of Truth)
const ExecuteABI = `
[{"type":"function","name":"ENGINE_EXECUTE",
  "inputs":[
    {"name":"grant","type":"tuple","components":[
      {"name":"from","type":"address"},
      {"name":"engine","type":"address"},
      {"name":"xrc729","type":"address"},
      {"name":"ostcId","type":"string"},
      {"name":"ostcHash","type":"bytes32"},
      {"name":"processId","type":"uint256"},
      {"name":"maxTotalGas","type":"uint256"},
      {"name":"expiry","type":"uint256"},
      {"name":"sessionId","type":"uint256"},
      {"name":"chainId","type":"uint256"}]},
    {"name":"call","type":"tuple","components":[
      {"name":"to","type":"address"},
      {"name":"data","type":"bytes"},
      {"name":"valueWei","type":"uint256"},
      {"name":"gasLimit","type":"uint64"},
      {"name":"validationGas","type":"uint64"},
      {"name":"maxFeePerGas","type":"uint256"},
      {"name":"deadline","type":"uint64"},
      {"name":"grantFeeSeconds","type":"uint64"},
      {"name":"grantFeePerYearWei","type":"uint256"}]},
    {"name":"meta","type":"tuple","components":[
      {"name":"iteration","type":"uint64"},
      {"name":"stepId","type":"string"},
      {"name":"ruleContract","type":"address"},
      {"name":"ruleHash","type":"bytes32"},
      {"name":"payload","type":"bytes"},
      {"name":"apiSaves","type":"bytes"},
      {"name":"contractSaves","type":"bytes"},
      {"name":"extras","type":"bytes"}]}],
  "outputs":[
    {"name":"success","type":"bool"},
    {"name":"gasUsed","type":"uint64"},
    {"name":"evmFee","type":"uint256"},
    {"name":"valFee","type":"uint256"}]},
 {"type":"function","name":"BILL_GRANTS_ONLY",
  "inputs":[
    {"name":"payer","type":"address"},
    {"name":"grantFeeSeconds","type":"uint64"},
    {"name":"grantFeePerYearWei","type":"uint256"}],
  "outputs":[{"name":"chargedWei","type":"uint256"}]}]`

const GetNextPidABI = `
[{"type":"function","name":"ENGINE_GET_NEXT_PID",
  "inputs":[{"name":"user","type":"address"}],
  "outputs":[{"name":"pid","type":"uint256"}]}]`

const IsPidUsedABI = `
[{"type":"function","name":"ENGINE_IS_PID_USED",
  "inputs":[{"name":"user","type":"address"},{"name":"pid","type":"uint256"}],
  "outputs":[{"name":"used","type":"bool"}]}]`

// Event-ABIs (ebenfalls zentral)
const EngineMetaEventABI = `
  [{"type":"event","name":"EngineMeta","inputs":[
    {"name":"SessionId","type":"uint256"},
    {"name":"iteration","type":"uint64"},
    {"name":"orchestration","type":"address"},
    {"name":"ostcId","type":"string"},
    {"name":"ostcHash","type":"bytes32"},
    {"name":"stepId","type":"string"},
    {"name":"ruleContract","type":"address"},
    {"name":"ruleHash","type":"bytes32"},
    {"name":"execContract","type":"address"},
    {"name":"execResult","type":"bool"},
    {"name":"payload","type":"bytes"},
    {"name":"apiSaves","type":"bytes"},
    {"name":"contractSaves","type":"bytes"}]}]`

const EngineExtrasEventABI = `
  [{"type":"event","name":"EngineExtrasV2","inputs":[
    {"name":"gasUsed","type":"uint256"},
    {"name":"extras","type":"bytes"}]}]`
