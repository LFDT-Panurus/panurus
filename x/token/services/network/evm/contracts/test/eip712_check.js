// Independent EIP-712 validation of the Go golden fixture using ethers v6.
//
// This proves the Go eip712 package (and, in Phase 2, the Solidity contract) produce a spec-correct
// EIP-712 digest, not merely a self-consistent one. Run:
//
//   npm init -y && npm install ethers@6
//   node eip712_check.js statedelta_digest_fixture.json
//
// Exit code 0 means every value (type hashes, domain separator, digest) matches the fixture's
// expected values produced by the Go side.
const fs = require("fs");
const { ethers } = require("ethers");

const fx = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));

const domain = {
  name: fx.domain.name,
  version: fx.domain.version,
  chainId: fx.domain.chainId,
  verifyingContract: fx.domain.verifyingContract,
};

// EIP712Domain is implicit in ethers; do not include it in types.
const types = {
  StateDelta: [
    { name: "anchor", type: "bytes32" },
    { name: "spentRefs", type: "bytes32[]" },
    { name: "outputs", type: "OutputToken[]" },
    { name: "metadataKeys", type: "bytes32[]" },
    { name: "metadataVals", type: "bytes[]" },
    { name: "tokenRequestHash", type: "bytes32" },
    { name: "publicParamsHash", type: "bytes32" },
    { name: "publicParamsVersion", type: "uint64" },
    { name: "isSetup", type: "bool" },
    { name: "setupParameters", type: "bytes" },
  ],
  OutputToken: [
    { name: "tokenID", type: "bytes32" },
    { name: "snMarker", type: "bytes32" },
    { name: "tokenData", type: "bytes" },
  ],
};

const sd = fx.stateDelta;
const value = {
  anchor: sd.anchor,
  spentRefs: sd.spentRefs,
  outputs: sd.outputs.map((o) => ({ tokenID: o.tokenID, snMarker: o.snMarker, tokenData: o.tokenData })),
  metadataKeys: sd.metadataKeys,
  metadataVals: sd.metadataVals,
  tokenRequestHash: sd.tokenRequestHash,
  publicParamsHash: sd.publicParamsHash,
  publicParamsVersion: sd.publicParamsVersion,
  isSetup: sd.isSetup,
  setupParameters: sd.setupParameters,
};

const enc = ethers.TypedDataEncoder;
const got = {
  outputTokenTypeHash: ethers.keccak256(ethers.toUtf8Bytes("OutputToken(bytes32 tokenID,bytes32 snMarker,bytes tokenData)")),
  stateDeltaTypeHash: ethers.keccak256(
    ethers.toUtf8Bytes(
      "StateDelta(bytes32 anchor,bytes32[] spentRefs,OutputToken[] outputs,bytes32[] metadataKeys,bytes[] metadataVals,bytes32 tokenRequestHash,bytes32 publicParamsHash,uint64 publicParamsVersion,bool isSetup,bytes setupParameters)OutputToken(bytes32 tokenID,bytes32 snMarker,bytes tokenData)",
    ),
  ),
  domainSeparator: enc.hashDomain(domain),
  digest: enc.hash(domain, types, value),
};

let ok = true;
for (const k of Object.keys(fx.expected)) {
  const match = got[k].toLowerCase() === fx.expected[k].toLowerCase();
  ok = ok && match;
  console.log(`${match ? "OK  " : "FAIL"}  ${k}`);
  console.log(`      go    : ${fx.expected[k]}`);
  console.log(`      ethers: ${got[k]}`);
}
console.log(ok ? "\nALL MATCH — Go EIP-712 is spec-correct" : "\nMISMATCH — Go EIP-712 deviates from ethers");
process.exit(ok ? 0 : 1);
