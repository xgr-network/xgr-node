// Reproducible private v3.5 contract candidate; does not deploy or select a fork.
import {createRequire} from 'node:module';
import {readFileSync, writeFileSync, mkdirSync} from 'node:fs';
import {execFileSync} from 'node:child_process';
import {createHash} from 'node:crypto';
import {fileURLToPath} from 'node:url';
import path from 'node:path';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const require = createRequire(process.env.XGR_SOLC_MODULE || new URL('./staking-pop-build/package.json', import.meta.url));
const solc = require('solc');
const version = solc.version();
if (version !== '0.8.30+commit.73712a01.Emscripten.clang') throw Error(`Unexpected solc: ${version}`);
const baseline = '0d9aca695c49784da64736d78620cc1bba4f7daa';
const sourcePath = 'contracts/staking/StakingV2.sol';
const settings = {
  optimizer: {enabled: true, runs: 200},
  evmVersion: 'shanghai',
  metadata: {bytecodeHash: 'none', appendCBOR: false},
  outputSelection: {'*': {'*': ['abi', 'evm.bytecode.object', 'evm.deployedBytecode.object', 'storageLayout']}}
};
function compile(source) {
  const input = {language: 'Solidity', sources: {[sourcePath]: {content: source}}, settings};
  const output = JSON.parse(solc.compile(JSON.stringify(input)));
  for (const error of output.errors || []) {
    if (error.severity === 'error') throw Error(error.formattedMessage);
  }
  return {input, contract: output.contracts[sourcePath].StakingV2};
}
function layoutType(id, types) {
  const t = types[id];
  const out = {label: t.label, encoding: t.encoding, numberOfBytes: t.numberOfBytes};
  for (const k of ['key','value','base']) if(t[k]) out[k] = layoutType(t[k], types);
  if(t.members) out.members = t.members.map(m => ({label: m.label, slot: m.slot, offset: m.offset, type: layoutType(m.type, types)}));
  return out;
}
function layout(contract) {
  return contract.storageLayout.storage.map(m => ({label:m.label, slot:m.slot, offset:m.offset, type:layoutType(m.type,contract.storageLayout.types)}));
}
const originalSource = execFileSync('git', ['show', `${baseline}:${sourcePath}`], {cwd:root, encoding:'utf8'});
const old = compile(originalSource);
const candidate = compile(readFileSync(path.join(root, sourcePath),'utf8'));
const before = layout(old.contract), after = layout(candidate.contract);
if(JSON.stringify(before) !== JSON.stringify(after.slice(0,before.length))) throw Error('Existing storage layout changed');
if(after.length !== before.length+3 || after[before.length].label !== '_blsPoPVerified' || after[before.length].slot !== '11' || after[before.length+1].label !== 'blsPoPNonce' || after[before.length+1].slot !== '12' || after[before.length+2].label !== 'blsPoPMigrationHash' || after[before.length+2].slot !== '13') throw Error('Unexpected appended PoP storage');
const oldHelper = execFileSync('git',['show',`${baseline}:helper/staking/staking.go`],{cwd:root,encoding:'utf8'});
const oldCreation = Buffer.from(oldHelper.match(/StakingSCBytecode = "([0-9a-f]+)"/)[1],'hex');
const oldRuntimeOffset = oldCreation.indexOf(Buffer.from([0xf3,0xfe]));
if(oldRuntimeOffset<0) throw Error('Legacy bootstrap runtime boundary missing');
const oldRuntime = oldCreation.subarray(oldRuntimeOffset+2);
const runtime = candidate.contract.evm.deployedBytecode.object;
if(runtime.length/2 > 24576 || runtime.length === 0) throw Error('Invalid runtime size');
const sha256 = value => createHash('sha256').update(value).digest('hex');
const artifact = {
  status:'PRIVATE_CANDIDATE_NOT_ACTIVATED', compiler:version, baseline,
  inputSha256:sha256(JSON.stringify(candidate.input)), settings,
  abi:candidate.contract.abi,
  creationBytecode:'0x'+candidate.contract.evm.bytecode.object,
  runtimeBytecode:'0x'+runtime,
  runtimeSha256:sha256(Buffer.from(runtime,'hex')),
  legacyRuntimeBytecode:'0x'+oldRuntime.toString('hex'),
  storageLayout:after, legacyStorageLayout:before
};
const destination = path.join(root,'contracts/staking/pop-artifact');
mkdirSync(destination,{recursive:true});
const filename = path.join(destination,'StakingV2PoP.json');
const serialized = JSON.stringify(artifact,null,2)+'\n';
if (process.argv.includes('--check')) {
  if (readFileSync(filename,'utf8') !== serialized) throw Error('Checked-in PoP artifact does not match reproducible compilation');
} else {
  writeFileSync(filename,serialized);
}
console.log(JSON.stringify({artifact:'contracts/staking/pop-artifact/StakingV2PoP.json', runtimeBytes:runtime.length/2, runtimeSha256:artifact.runtimeSha256, storagePrefixCompatible:true}));
