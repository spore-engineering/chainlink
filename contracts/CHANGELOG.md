# @chainlink/contracts CHANGELOG.md

## Unreleased

### Added:

- `@chainlink/contracts` package changelog.
- `KeeperCompatibleInterface` contracts.
- Feeds Registry contracts: `FeedRegistryInterface` and `Denominations`.
- v0.8 Consumable contracts (`ChainlinkClient`, `VRFConsumerBase` and aggregator interfaces).
- Multi-word response handling in v0.7 and v0.8 `ChainlinkClient` contracts.

### Changed:

- Added missing licensees to `KeeperComptibleInterface`'s
- Upgrade solidity v8 compiler version from 0.8.4 to 0.8.6
- Tests converted to Hardhat.
- Ethers upgraded from v4 to v5.
- JSON contract artifacts in `abi/` are now [generated by Hardhat](https://hardhat.org/guides/compile-contracts.html) instead of `0x/sol-compiler`. Because of this, format of these files has changed to `hh-sol-artifact-1`.

### Removed:

- Removed dependencies: `@chainlink/belt`, `@chainlink/test-helpers` and `@truffle`.
- Ethers and Truffle contract artifacts are no longer published.
