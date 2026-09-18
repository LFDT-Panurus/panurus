/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package finality tracks EVM transaction finality and notifies registered listeners.
//
// The baseline determines finality from receipts read at the "finalized" block tag, resolving an
// anchor to its Ethereum transaction via the indexed StateCommitted event; the fabric-x-evm
// gateway's TransactionByHash().isPending lifecycle is layered on as an efficiency signal where
// available. It reuses the backend-agnostic pieces from token/services/network/fabricx/finality
// (the event queue and OnlyOnceListener). Implemented in Phase 5/6.
package finality
