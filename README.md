<p align="center">
  <img src="docs/imgs/Panurus_white.png" alt="Panurus" width="450"/>
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202-blue" alt="License"></a>
  <a href="https://goreportcard.com/badge/github.com/LFDT-Panurus/panurus"><img src="https://goreportcard.com/badge/github.com/LFDT-Panurus/panurus" alt="Go Report Card"></a>
  <a href="https://github.com/LFDT-Panurus/panurus/actions/workflows/tests.yml"><img src="https://github.com/LFDT-Panurus/panurus/actions/workflows/tests.yml/badge.svg?branch=main" alt="Tests"></a>
  <a href="https://github.com/LFDT-Panurus/panurus/actions/workflows/codeql-analysis.yml"><img src="https://github.com/LFDT-Panurus/panurus/actions/workflows/codeql-analysis.yml/badge.svg?branch=main" alt="CodeQL"></a>
  <a href="https://coveralls.io/github/LFDT-Panurus/panurus?branch=main"><img src="https://coveralls.io/repos/github/LFDT-Panurus/panurus/badge.svg?branch=main" alt="Coverage Status"></a>
</p>

Panurus provides a collection of APIs and services that streamline development for token-based distributed applications.

# Useful Links
 
- [`Documentation`](docs/README.md): The entry point for Panurus documentation.
- [`Development`](docs/development/development.md): All about the development guidelines.
- [`Contributing`](CONTRIBUTING.md): How to contribute to the project.
- [`Fabric Samples`](https://github.com/hyperledger/fabric-samples/tree/main/token-sdk) Panurus sample application is the
  quickest way to get a full network running with a REST API to issue, transfer and redeem tokens right away.
- [`Benchmarks`](./docs/drivers/benchmark/benchmark.md): Benchmark guidelines and reports.
- `Feedback`: Your help is the key to the success of Panurus. 
  - Submit your issues [`here`][`panurus` Issues]. 
  - Found a bug? Need help to fix an issue? You have a great idea for a new feature? Talk to us! You can reach us on
    [Discord](https://discord.gg/hyperledger) in #panurus.
  
- [`Fabric Smart Client`](https://github.com/hyperledger-labs/fabric-smart-client): Panurus leverages the 
  `Fabric Smart Client` for transaction orchestration, storing tokens and wallets, and more. Check it out.

# Additional Resources

- (March 17, 2022) [`Hyperledger in-Depth: Tokens in Hyperledger Fabric: What’s possible today and what’s coming`](https://www.hyperledger.org/learn/webinars/hyperledger-in-depth-tokens-in-hyperledger-fabric-whats-possible-today-and-whats-coming):
  Tokenizing the physical world is a hot blockchain topic in the industry, especially as it relates to the 
  trade of tokens as a basis of new forms of commerce. In this Hyperledger Foundation member webinar, 
  the IBM Research team describes in this webinar what tokenization use cases are possible with Hyperledger Fabric today, 
  and what enhancements are in the works (aka Panurus).
- (October 12, 2023) [How to create a currency management app and deploy it to a Hyperledger Fabric network](https://www.youtube.com/watch?v=PX9SDva97vQ):
  In this comprehensive guide, we'll walk you through two essential aspects of Panurus. Firstly, you'll learn how to develop a straightforward token application to manage a currency. You'll grasp the fundamentals of creating tokens, and implementing transaction logic using Panurus. Once you've mastered the application development, we'll then show you how to effortlessly deploy it in your existing Fabric network, ensuring a seamless integration with your blockchain infrastructure. By the end of this tutorial, you'll be equipped with the skills to expand your blockchain capabilities and unleash the true potential of decentralized currency management. (Refers to [Fabric Samples](https://github.com/hyperledger/fabric-samples/tree/main/token-sdk))

# Disclaimer

`Panurus` has not been audited and is provided as-is, use at your own risk.
The project will be subject to rapid changes to complete the open-sourcing process, and  the list of features.

# Motivation

**Hyperledger Fabric: Blockchain Built for Business**

Hyperledger Fabric ([https://hyperledger-fabric.readthedocs.io/](https://hyperledger-fabric.readthedocs.io/)) is an open-source platform designed for permissioned blockchain networks. It offers a modular and extensible architecture, allowing for customization and future growth.  Unlike traditional blockchains, Fabric applications can be written in any general-purpose programming language, making them more accessible to developers.

**Beyond Cryptocurrencies: Tokenizing the World**

While blockchain is often associated with cryptocurrencies, its potential extends far beyond. Fabric allows for the creation of tokens that represent real-world assets, both fungible (like loyalty points) and non-fungible (like unique digital artwork). This opens doors for new business models and unlocks additional value from existing assets.

**The Challenge: Building Tokenized Applications**

Developing applications that leverage tokens on Hyperledger Fabric can be complex. Fabric lacked a built-in SDK for creating and managing tokens, forcing developers to build solutions from scratch.  This not only led to wasted effort with duplicated code, but also exposed applications to potential security vulnerabilities.

**Introducing Panurus: Streamlining Tokenized Development (and Beyond)**

Panurus has evolved beyond its initial focus on Hyperledger Fabric. It now empowers developers with the following capabilities across various platforms, including permissioned blockchains like Fabric:

* **Tokenization Made Easy:** Create tokens representing any type of asset, be it physical or digital.
* **Privacy by Design:** Select the appropriate privacy level for your specific use case, without modifying your application logic.
* **Peer-to-Peer Transactions:** Orchestrate token transfers directly between users, streamlining the process.
* **Atomic Swaps:** Facilitate secure exchanges of different tokens without relying on intermediaries.
* **Transaction Auditing:** Review transactions before they are finalized, ensuring accuracy and compliance.
* **Interoperability:** Connect with token systems on other blockchain networks, fostering broader ecosystems.
* **Seamless Integration:** Add a token layer to existing applications, regardless of platform, with minimal effort.

With Panurus, developing secure and efficient enterprise-grade tokenized applications becomes a reality, offering flexibility for developers to choose the platform that best suits their needs.

# License

This project is licensed under the Apache 2 License - see the [`LICENSE`](LICENSE) file for details

[`Panurus` Issues]: https://github.com/LFDT-Panurus/panurus/issues
[#panurus in Discord]: https://discord.gg/hyperledger
