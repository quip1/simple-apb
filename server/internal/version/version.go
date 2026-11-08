// SPDX-License-Identifier: AGPL-3.0-or-later

// Package version holds build information, set with -ldflags at build time:
//
//	go build -ldflags "-X github.com/simple-apb/simple-apb/server/internal/version.Version=1.2.3"
package version

// Version is the server's semver. "dev" for local builds.
var Version = "dev"

// APIVersion is the REST API major version served under /api/<APIVersion>.
const APIVersion = "v1"
