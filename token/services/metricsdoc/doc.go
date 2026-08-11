/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package metricsdoc guards the metrics reference in docs/development/metrics.md.
//
// The SDK never spells out the metric names it exports: the fully-qualified
// Prometheus name of a metric is assembled at registration time from the Go
// package that creates it, so the same CounterOpts produce different exported
// names depending on which package - and which provider wrapper - the metric
// travels through. Documentation written from the bare Name field of the opts
// therefore lists names that no Prometheus query will ever match.
//
// The tests in this package instantiate every metrics constructor of the SDK the
// way production wiring does, read the resulting names back out of a Prometheus
// registry, and compare them both against a golden file and against the
// reference documentation. Adding, renaming or relocating a metric fails the
// test until the documentation is updated.
//
// Which provider a constructor receives is as much a part of the exported name
// as the opts are, so it is checked rather than assumed: the token drivers are
// pinned as the only place a TMS-scoped provider is built, and every production
// call site listed for a constructor must still contain that call. Removing the
// wrapper, or moving a call site, therefore fails here instead of quietly
// renaming twenty-one metrics.
package metricsdoc
